package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"qoder2api/internal/cosy"
	"qoder2api/logger"
)

func (b *Bridge) HandleCodexResponses(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteCodexErr(w, responsesBadRequest("cannot read request body: %s", err))
		return
	}

	reqID := cosy.NewUUID()[:8]
	logger.Info("[Codex][%s] 收到请求 %s %s body_size=%d", reqID, r.Method, r.URL.Path, len(body))
	logger.Debug("[Codex][%s] 请求体（敏感字段已脱敏）: %s", reqID, RedactRequestBodyJSON(body))

	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil || req == nil {
		WriteCodexErr(w, responsesBadRequest("request body must be a JSON object"))
		return
	}
	if err := validateResponsesRequest(req); err != nil {
		WriteCodexErr(w, err)
		return
	}
	opts, err := ParseCallOpts(req, "responses")
	if err != nil {
		WriteCodexErr(w, err)
		return
	}
	model := StrValDefault(req, "model", "Auto")
	opts.model, err = b.resolveModel("codex", model)
	if err != nil {
		WriteCodexErr(w, err)
		return
	}
	model = opts.model.DisplayName
	tools, err := responsesTools(req["tools"])
	if err != nil {
		WriteCodexErr(w, err)
		return
	}
	if choice, ok := req["tool_choice"]; ok && choice != nil {
		choice, err = responsesToolChoice(choice)
		if err != nil {
			WriteCodexErr(w, err)
			return
		}
		if opts.Parameters == nil {
			opts.Parameters = make(map[string]interface{})
		}
		opts.Parameters["tool_choice"] = choice
	}

	stream, _ := req["stream"].(bool)
	instructions, _ := req["instructions"].(string)
	messages := CodexInputToMessages(req["input"], instructions)
	hasSystem := false
	for _, raw := range messages {
		if message, ok := raw.(map[string]interface{}); ok && message["role"] == "system" {
			hasSystem = true
			break
		}
	}
	if !hasSystem {
		messages = append(BuildQoderMessages(b.templateMessages(), nil, "", tools != nil), messages...)
	}

	state := newResponsesState(model)
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		state.emit = func(eventType string, data map[string]interface{}) {
			data["type"] = eventType
			data["sequence_number"] = state.sequence
			state.sequence++
			encoded, marshalErr := json.Marshal(data)
			if marshalErr != nil {
				return
			}
			if _, writeErr := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, encoded); writeErr != nil {
				if state.err == nil {
					state.err = writeErr
				}
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		state.send("response.created", map[string]interface{}{"response": state.response("in_progress", nil)})
		state.send("response.in_progress", map[string]interface{}{"response": state.response("in_progress", nil)})
	}

	err = b.CallQoderWithOpts(r.Context(), "codex", messages, model, tools, opts, state.addDelta)
	if err == nil {
		err = state.err
	}
	if err == nil && r.Context().Err() != nil {
		err = r.Context().Err()
	}
	status, err := state.finish(err)
	if err != nil {
		logger.Error("[Codex][%s] 请求失败: %v (耗时 %dms)", reqID, err, time.Since(startTime).Milliseconds())
		if !stream {
			WriteCodexErr(w, err)
			return
		}
		state.closeItems("incomplete")
		state.send("response.failed", map[string]interface{}{"response": state.response(status, err)})
		return
	}

	state.closeItems(status)
	response := state.response(status, nil)
	if stream {
		eventType := "response.completed"
		if status == "incomplete" {
			eventType = "response.incomplete"
		}
		state.send(eventType, map[string]interface{}{"response": response})
	} else {
		WriteJSON(w, response)
	}
	logger.Info("[Codex][%s] 完成 status=%s output_items=%d 耗时=%dms", reqID, status, len(state.items), time.Since(startTime).Milliseconds())
}

func responsesBadRequest(format string, args ...interface{}) error {
	message := fmt.Sprintf(format, args...)
	return &UpstreamError{Status: http.StatusBadRequest, Detail: message, ErrType: "invalid_request_error", Message: message}
}

func validateResponsesRequest(req map[string]interface{}) error {
	for _, key := range []string{"previous_response_id", "conversation"} {
		if value := req[key]; value != nil && value != "" {
			return responsesBadRequest("%s is unsupported: supply the full input history for this stateless endpoint", key)
		}
	}
	for _, key := range []string{"background", "store", "stream"} {
		if value, exists := req[key]; exists && value != nil {
			enabled, ok := value.(bool)
			if !ok {
				return responsesBadRequest("%s must be a boolean", key)
			}
			if key != "stream" && enabled {
				return responsesBadRequest("%s=true is unsupported by this stateless endpoint", key)
			}
		}
	}
	if value := req["instructions"]; value != nil {
		if _, ok := value.(string); !ok {
			return responsesBadRequest("instructions must be a string")
		}
	}
	if value := req["model"]; value != nil {
		if model, ok := value.(string); !ok || model == "" {
			return responsesBadRequest("model must be a nonempty string")
		}
	}
	if _, ok := req["input"].(string); ok {
		return nil
	}
	items, ok := req["input"].([]interface{})
	if !ok {
		return responsesBadRequest("input must be a string or an array of input items")
	}
	for i, raw := range items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			return responsesBadRequest("input[%d] must be an object", i)
		}
		kind, _ := item["type"].(string)
		switch kind {
		case "", "message":
			role, _ := item["role"].(string)
			switch role {
			case "user", "assistant", "system", "developer":
			default:
				return responsesBadRequest("input[%d] has an unsupported message role", i)
			}
			if err := validateResponsesContent(item["content"]); err != nil {
				return responsesBadRequest("input[%d].content: %s", i, err)
			}
		case "function_call":
			for _, key := range []string{"call_id", "name", "arguments"} {
				value, ok := item[key].(string)
				if !ok || (key != "arguments" && value == "") {
					return responsesBadRequest("input[%d].%s must be a string (call_id and name cannot be empty)", i, key)
				}
			}
		case "function_call_output":
			if callID, ok := item["call_id"].(string); !ok || callID == "" {
				return responsesBadRequest("input[%d].call_id must be a nonempty string", i)
			}
			if err := validateResponsesContent(item["output"]); err != nil {
				return responsesBadRequest("input[%d].output: %s", i, err)
			}
		case "reasoning":
			summary, ok := item["summary"].([]interface{})
			if !ok {
				return responsesBadRequest("input[%d].summary must be an array of summary_text parts", i)
			}
			for _, rawPart := range summary {
				part, ok := rawPart.(map[string]interface{})
				if !ok || part["type"] != "summary_text" {
					return responsesBadRequest("input[%d].summary must contain summary_text parts", i)
				}
				if _, ok := part["text"].(string); !ok {
					return responsesBadRequest("input[%d].summary text must be a string", i)
				}
			}
			if encrypted, _ := item["encrypted_content"].(string); encrypted != "" {
				return responsesBadRequest("encrypted reasoning history is unsupported; supply reasoning summaries instead")
			}
		default:
			return responsesBadRequest("input[%d] has unsupported type %q", i, kind)
		}
	}
	return nil
}

func validateResponsesContent(content interface{}) error {
	if _, ok := content.(string); ok {
		return nil
	}
	parts, ok := content.([]interface{})
	if !ok {
		return fmt.Errorf("must be a string or an array of text/image parts")
	}
	for _, raw := range parts {
		part, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("content parts must be objects")
		}
		switch part["type"] {
		case "input_text", "output_text", "text":
			if _, ok := part["text"].(string); !ok {
				return fmt.Errorf("text parts require a string text field")
			}
		case "input_image", "image_url", "image":
			if len(ExtractMessageImages(map[string]interface{}{"content": []interface{}{part}})) == 0 {
				return fmt.Errorf("image parts require a supported data:, http: or https: image URL")
			}
		default:
			return fmt.Errorf("unsupported content part type %v", part["type"])
		}
	}
	return nil
}

func responsesTools(raw interface{}) (interface{}, error) {
	if raw == nil {
		return nil, nil
	}
	tools, ok := raw.([]interface{})
	if !ok {
		return nil, responsesBadRequest("tools must be an array")
	}
	if len(tools) == 0 {
		return nil, nil
	}
	converted := make([]interface{}, 0, len(tools))
	for i, rawTool := range tools {
		tool, ok := rawTool.(map[string]interface{})
		if !ok || tool["type"] != "function" {
			return nil, responsesBadRequest("tools[%d]: only function tools are supported", i)
		}
		function, nested := tool["function"].(map[string]interface{})
		if !nested {
			function = make(map[string]interface{})
			for _, key := range []string{"name", "description", "parameters", "strict"} {
				if value, exists := tool[key]; exists {
					function[key] = value
				}
			}
		}
		if name, ok := function["name"].(string); !ok || name == "" {
			return nil, responsesBadRequest("tools[%d] requires a nonempty function name", i)
		}
		converted = append(converted, map[string]interface{}{"type": "function", "function": function})
	}
	return converted, nil
}

func responsesToolChoice(raw interface{}) (interface{}, error) {
	if choice, ok := raw.(string); ok {
		switch choice {
		case "auto", "none", "required":
			return choice, nil
		}
	}
	if choice, ok := raw.(map[string]interface{}); ok && choice["type"] == "function" {
		name, _ := choice["name"].(string)
		if function, ok := choice["function"].(map[string]interface{}); ok {
			name, _ = function["name"].(string)
		}
		if name != "" {
			return map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": name}}, nil
		}
	}
	return nil, responsesBadRequest("tool_choice must be auto, none, required, or a named function")
}

// CodexInputToMessages converts stateless Responses history to Qoder messages.
// The HTTP handler validates supported item/content shapes before conversion.
func CodexInputToMessages(input interface{}, instructions string) []interface{} {
	var messages []interface{}
	if instructions != "" {
		messages = append(messages, BuildStructuredMessage("system", instructions))
	}
	if text, ok := input.(string); ok {
		return append(messages, BuildUserMessage(text))
	}
	items, _ := input.([]interface{})
	var reasoning strings.Builder
	flushReasoning := func() {
		if reasoning.Len() != 0 {
			message := BuildStructuredMessage("assistant", "")
			message["reasoning_content"] = reasoning.String()
			messages = append(messages, message)
			reasoning.Reset()
		}
	}
	for _, raw := range items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		kind, _ := item["type"].(string)
		switch kind {
		case "reasoning":
			summary, _ := item["summary"].([]interface{})
			for _, rawPart := range summary {
				part, _ := rawPart.(map[string]interface{})
				text, _ := part["text"].(string)
				if reasoning.Len() > 0 && text != "" {
					reasoning.WriteString("\n")
				}
				reasoning.WriteString(text)
			}
		case "", "message":
			role, _ := item["role"].(string)
			if role == "developer" {
				role = "system"
			}
			if role != "assistant" {
				flushReasoning()
			}
			message := responsesHistoryMessage(role, item["content"])
			if role == "assistant" && reasoning.Len() > 0 {
				message["reasoning_content"] = reasoning.String()
				reasoning.Reset()
			}
			messages = append(messages, message)
		case "function_call":
			var message map[string]interface{}
			if len(messages) > 0 {
				last, _ := messages[len(messages)-1].(map[string]interface{})
				if last["role"] == "assistant" {
					message = last
				}
			}
			if message == nil {
				message = BuildStructuredMessage("assistant", "")
				messages = append(messages, message)
			}
			if reasoning.Len() > 0 {
				previous, _ := message["reasoning_content"].(string)
				message["reasoning_content"] = previous + reasoning.String()
				reasoning.Reset()
			}
			calls, _ := message["tool_calls"].([]interface{})
			message["tool_calls"] = append(calls, map[string]interface{}{
				"id": item["call_id"], "type": "function",
				"function": map[string]interface{}{"name": item["name"], "arguments": item["arguments"]},
			})
		case "function_call_output":
			flushReasoning()
			message := responsesHistoryMessage("tool", item["output"])
			message["tool_call_id"] = item["call_id"]
			messages = append(messages, message)
		}
	}
	flushReasoning()
	return messages
}

func responsesHistoryMessage(role string, content interface{}) map[string]interface{} {
	incoming := map[string]interface{}{"role": role, "content": content}
	text := NormalizeMessageContent(incoming)
	images := ExtractMessageImages(incoming)
	if role == "user" || len(images) > 0 {
		message := BuildUserMessageWithImages(text, images)
		message["role"] = role
		return message
	}
	return BuildStructuredMessage(role, text)
}

func responsesCallError(err error) error {
	if errors.Is(err, context.Canceled) {
		return NewUpstreamError(499, "request cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewUpstreamError(http.StatusGatewayTimeout, "upstream request deadline exceeded")
	}
	return err
}

func WriteCodexErr(w http.ResponseWriter, err error) {
	err = responsesCallError(err)
	errMsg, errType := FriendlyError(err)
	status := ErrorStatus(err)
	if status == http.StatusBadRequest && errType != ErrTypeContentPolicy {
		errType = "invalid_request_error"
	} else if errType == "" || errType == "qoder_error" {
		errType = "server_error"
	}
	body, _ := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{"message": errMsg, "type": errType},
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

package bridge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"qoder2api/internal/cosy"
)

// responsesState is shared by streaming and nonstreaming requests: events and
// the final response are projections of the same accumulated upstream deltas.
// Only observed usage and terminal frames are used; EOF is not completion.
type responsesState struct {
	id           string
	model        string
	createdAt    int64
	sequence     int
	emit         func(string, map[string]interface{})
	items        []*responsesOutputItem
	text         *responsesOutputItem
	reasoning    *responsesOutputItem
	toolsByIndex map[int]*responsesOutputItem
	toolsByID    map[string]*responsesOutputItem
	terminal     bool
	finishReason string
	hasUsage     bool
	inputTokens  int
	outputTokens int
	cachedTokens int
	reasonTokens int
	err          error
}

type responsesOutputItem struct {
	id        string
	kind      string
	index     int
	status    string
	announced bool
	text      strings.Builder
	callID    string
	name      string
	arguments strings.Builder
}

func newResponsesState(model string) *responsesState {
	return &responsesState{
		id: "resp_" + cosy.NewRequestID(), model: model, createdAt: time.Now().Unix(),
		toolsByIndex: make(map[int]*responsesOutputItem),
		toolsByID:    make(map[string]*responsesOutputItem),
	}
}

func (s *responsesState) send(event string, data map[string]interface{}) {
	if s.emit != nil {
		s.emit(event, data)
	}
}

func (s *responsesState) newItem(kind, prefix string) *responsesOutputItem {
	item := &responsesOutputItem{
		id: prefix + cosy.NewRequestID(), kind: kind, index: len(s.items), status: "in_progress",
	}
	s.items = append(s.items, item)
	return item
}

func (item *responsesOutputItem) value() map[string]interface{} {
	value := map[string]interface{}{"id": item.id, "type": item.kind, "status": item.status}
	switch item.kind {
	case "message":
		value["role"] = "assistant"
		value["content"] = []interface{}{responsesTextPart(item.text.String())}
	case "reasoning":
		value["summary"] = []interface{}{responsesSummaryPart(item.text.String())}
	case "function_call":
		if item.callID != "" {
			value["call_id"] = item.callID
		}
		if item.name != "" {
			value["name"] = item.name
		}
		value["arguments"] = item.arguments.String()
	}
	return value
}

func responsesTextPart(text string) map[string]interface{} {
	return map[string]interface{}{"type": "output_text", "text": text, "annotations": []interface{}{}}
}

func responsesSummaryPart(text string) map[string]interface{} {
	return map[string]interface{}{"type": "summary_text", "text": text}
}

func (s *responsesState) announce(item *responsesOutputItem) {
	if item.announced {
		return
	}
	item.announced = true
	if s.emit != nil {
		value := item.value()
		switch item.kind {
		case "message":
			value["content"] = []interface{}{}
		case "reasoning":
			value["summary"] = []interface{}{}
		case "function_call":
			value["arguments"] = ""
		}
		s.send("response.output_item.added", map[string]interface{}{"output_index": item.index, "item": value})
	}
}

func (s *responsesState) addDelta(delta Delta) {
	if s.err != nil {
		return
	}
	if delta.Err != nil {
		s.err = delta.Err
		return
	}
	if delta.HasUsage {
		s.hasUsage = true
		s.inputTokens = delta.InputTokens
		s.outputTokens = delta.OutputTokens
		s.cachedTokens = delta.CachedTokens
		s.reasonTokens = delta.ReasoningTokens
	}
	if delta.Done || delta.FinishReason != "" {
		s.terminal = true
	}
	if delta.FinishReason != "" {
		s.finishReason = delta.FinishReason
	}
	if delta.Reasoning != "" {
		if s.reasoning == nil {
			s.reasoning = s.newItem("reasoning", "rs_")
			s.announce(s.reasoning)
			if s.emit != nil {
				s.send("response.reasoning_summary_part.added", map[string]interface{}{
					"item_id": s.reasoning.id, "output_index": s.reasoning.index, "summary_index": 0,
					"part": responsesSummaryPart(""),
				})
			}
		}
		s.reasoning.text.WriteString(delta.Reasoning)
		if s.emit != nil {
			s.send("response.reasoning_summary_text.delta", map[string]interface{}{
				"item_id": s.reasoning.id, "output_index": s.reasoning.index, "summary_index": 0,
				"delta": delta.Reasoning,
			})
		}
	}
	if delta.Content != "" {
		if s.text == nil {
			s.text = s.newItem("message", "msg_")
			s.announce(s.text)
			if s.emit != nil {
				s.send("response.content_part.added", map[string]interface{}{
					"item_id": s.text.id, "output_index": s.text.index, "content_index": 0,
					"part": responsesTextPart(""),
				})
			}
		}
		s.text.text.WriteString(delta.Content)
		if s.emit != nil {
			s.send("response.output_text.delta", map[string]interface{}{
				"item_id": s.text.id, "output_index": s.text.index, "content_index": 0,
				"delta": delta.Content,
			})
		}
	}
	for _, raw := range delta.ToolCalls {
		if err := s.addToolDelta(raw); err != nil {
			s.err = err
			return
		}
	}
}

func (s *responsesState) addToolDelta(raw interface{}) error {
	chunk, ok := raw.(map[string]interface{})
	if !ok {
		return NewUpstreamError(http.StatusBadGateway, "upstream tool-call delta is not an object")
	}
	if kind, _ := chunk["type"].(string); kind != "" && kind != "function" {
		return NewUpstreamError(http.StatusBadGateway, "upstream returned an unsupported tool-call type")
	}
	callID, _ := chunk["id"].(string)
	if callID == "" {
		callID, _ = chunk["call_id"].(string)
	}
	index, hasIndex := responsesToolIndex(chunk["index"])
	if _, exists := chunk["index"]; exists && !hasIndex {
		return NewUpstreamError(http.StatusBadGateway, "upstream tool-call index is not a nonnegative integer")
	}
	var item *responsesOutputItem
	if hasIndex {
		item = s.toolsByIndex[index]
	}
	if callID != "" {
		byID := s.toolsByID[callID]
		if item != nil && byID != nil && item != byID {
			return NewUpstreamError(http.StatusBadGateway, "upstream tool-call index and call_id identify different calls")
		}
		if item == nil {
			item = byID
		}
	}
	if item == nil {
		if !hasIndex && callID == "" {
			return NewUpstreamError(http.StatusBadGateway, "upstream tool-call delta lacks both index and call_id")
		}
		item = s.newItem("function_call", "fc_")
	}
	if callID != "" {
		if item.callID != "" && item.callID != callID {
			return NewUpstreamError(http.StatusBadGateway, "upstream changed a tool call's call_id")
		}
		item.callID = callID
		s.toolsByID[callID] = item
	}
	if hasIndex {
		s.toolsByIndex[index] = item
	}
	function, ok := chunk["function"].(map[string]interface{})
	if value, exists := chunk["function"]; exists && value != nil && !ok {
		return NewUpstreamError(http.StatusBadGateway, "upstream tool-call function data is not an object")
	}
	if name, ok := function["name"].(string); ok {
		item.name += name
	} else if function["name"] != nil {
		return NewUpstreamError(http.StatusBadGateway, "upstream function name delta is not a string")
	}
	arguments, ok := function["arguments"].(string)
	if rawArguments, exists := function["arguments"]; exists && rawArguments != nil && !ok {
		return NewUpstreamError(http.StatusBadGateway, "upstream function arguments delta is not a string")
	}
	item.arguments.WriteString(arguments)
	if !item.announced {
		// IDs are never fabricated. Buffer argument fragments until the upstream
		// supplies enough metadata to expose a valid function_call item.
		if item.callID != "" && item.name != "" {
			s.announce(item)
			if s.emit != nil && item.arguments.Len() > 0 {
				s.send("response.function_call_arguments.delta", map[string]interface{}{
					"item_id": item.id, "output_index": item.index, "delta": item.arguments.String(),
				})
			}
		}
	} else if s.emit != nil && arguments != "" {
		s.send("response.function_call_arguments.delta", map[string]interface{}{
			"item_id": item.id, "output_index": item.index, "delta": arguments,
		})
	}
	return nil
}

func responsesToolIndex(raw interface{}) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, value >= 0
	case float64:
		index := int(value)
		return index, index >= 0 && float64(index) == value
	}
	return 0, false
}

func (s *responsesState) finish(err error) (string, error) {
	if err != nil {
		status := "failed"
		if errors.Is(err, context.Canceled) || ErrorStatus(err) == 499 {
			status = "cancelled"
		}
		return status, responsesCallError(err)
	}
	if !s.terminal {
		return "failed", NewUpstreamError(http.StatusBadGateway, "upstream stream ended before a terminal frame")
	}
	switch s.finishReason {
	case "", "stop", "tool_calls", "function_call", "end_turn":
	case "length", "max_tokens", "max_output_tokens", "content_filter":
		return "incomplete", nil
	case "cancelled", "canceled":
		return "cancelled", NewUpstreamError(499, "upstream cancelled the response")
	case "error", "failed":
		return "failed", NewUpstreamError(http.StatusBadGateway, "upstream failed the response")
	default:
		return "failed", NewUpstreamError(http.StatusBadGateway, fmt.Sprintf("unsupported upstream finish_reason %q", s.finishReason))
	}
	for _, item := range s.items {
		if item.kind == "function_call" && (item.callID == "" || item.name == "") {
			return "failed", NewUpstreamError(http.StatusBadGateway, "upstream ended a function call without its call_id or name")
		}
	}
	return "completed", nil
}

func (s *responsesState) closeItems(status string) {
	itemStatus := "completed"
	if status != "completed" {
		itemStatus = "incomplete"
	}
	for _, item := range s.items {
		if s.emit == nil {
			item.status = itemStatus
			continue
		}
		if !item.announced {
			s.announce(item)
			if item.kind == "function_call" && item.arguments.Len() > 0 {
				s.send("response.function_call_arguments.delta", map[string]interface{}{
					"item_id": item.id, "output_index": item.index, "delta": item.arguments.String(),
				})
			}
		}
		item.status = itemStatus
		switch item.kind {
		case "message":
			s.send("response.output_text.done", map[string]interface{}{
				"item_id": item.id, "output_index": item.index, "content_index": 0,
				"text": item.text.String(),
			})
			s.send("response.content_part.done", map[string]interface{}{
				"item_id": item.id, "output_index": item.index, "content_index": 0,
				"part": responsesTextPart(item.text.String()),
			})
		case "reasoning":
			s.send("response.reasoning_summary_text.done", map[string]interface{}{
				"item_id": item.id, "output_index": item.index, "summary_index": 0,
				"text": item.text.String(),
			})
			s.send("response.reasoning_summary_part.done", map[string]interface{}{
				"item_id": item.id, "output_index": item.index, "summary_index": 0,
				"part": responsesSummaryPart(item.text.String()),
			})
		case "function_call":
			s.send("response.function_call_arguments.done", map[string]interface{}{
				"item_id": item.id, "output_index": item.index, "name": item.name,
				"arguments": item.arguments.String(),
			})
		}
		s.send("response.output_item.done", map[string]interface{}{"output_index": item.index, "item": item.value()})
	}
}

func (s *responsesState) response(status string, err error) map[string]interface{} {
	output := make([]interface{}, 0, len(s.items))
	for _, item := range s.items {
		output = append(output, item.value())
	}
	response := map[string]interface{}{
		"id": s.id, "object": "response", "created_at": s.createdAt,
		"model": s.model, "status": status, "output": output,
		"error": nil, "incomplete_details": nil, "usage": nil,
		"store": false, "background": false,
	}
	if s.hasUsage {
		response["usage"] = map[string]interface{}{
			"input_tokens": s.inputTokens, "output_tokens": s.outputTokens,
			"total_tokens":          s.inputTokens + s.outputTokens,
			"input_tokens_details":  map[string]interface{}{"cached_tokens": s.cachedTokens},
			"output_tokens_details": map[string]interface{}{"reasoning_tokens": s.reasonTokens},
		}
	}
	if status == "incomplete" {
		reason := "max_output_tokens"
		if s.finishReason == "content_filter" {
			reason = "content_filter"
		}
		response["incomplete_details"] = map[string]interface{}{"reason": reason}
	}
	if err != nil {
		message, code := FriendlyError(err)
		if code == "" || code == "qoder_error" {
			code = "server_error"
		}
		response["error"] = map[string]interface{}{"code": code, "message": message}
	}
	return response
}

package bridge

import (
	"fmt"
	"math"
	"strings"
)

func invalidRequest(format string, args ...interface{}) error {
	message := fmt.Sprintf(format, args...)
	return &UpstreamError{Status: 400, Detail: message, ErrType: "invalid_request_error", Message: message}
}

// ParseCallOpts converts client reasoning and generation controls into Qoder's
// request parameters. Model capability is distinct from its default mode.
func ParseCallOpts(req map[string]interface{}, protocol string) (CallOpts, error) {
	opts := CallOpts{Parameters: make(map[string]interface{})}
	maxField := "max_tokens"
	switch protocol {
	case "responses":
		maxField = "max_output_tokens"
	case "chat":
		if _, exists := req["max_completion_tokens"]; exists {
			maxField = "max_completion_tokens"
		}
	}
	if value, exists := req[maxField]; exists && value != nil {
		n, ok := value.(float64)
		if !ok || n <= 0 || n != math.Trunc(n) || n >= float64(int(^uint(0)>>1)) {
			return opts, invalidRequest("%s must be a positive integer", maxField)
		}
		opts.MaxTokens = int(n)
	}

	var effort interface{}
	if value, exists := req["reasoning_effort"]; exists {
		effort = value
	}
	if value, exists := req["reasoning"]; exists && value != nil {
		reasoning, ok := value.(map[string]interface{})
		if !ok {
			return opts, invalidRequest("reasoning must be an object")
		}
		if value, exists := reasoning["effort"]; exists {
			effort = value
		}
	}
	if value, exists := req["thinking"]; exists && value != nil {
		thinking, ok := value.(map[string]interface{})
		if !ok {
			return opts, invalidRequest("thinking must be an object")
		}
		opts.ReasoningSet = true
		switch thinking["type"] {
		case "enabled", "adaptive":
			opts.IsReasoning = true
		case "disabled":
			effort = "none"
		default:
			return opts, invalidRequest("thinking.type must be enabled, adaptive or disabled")
		}
	}
	if protocol == "claude" {
		if config, ok := req["output_config"].(map[string]interface{}); ok {
			if value, exists := config["effort"]; exists {
				effort = value
			}
		}
	}
	if effort != nil {
		value, ok := effort.(string)
		if !ok {
			return opts, invalidRequest("reasoning effort must be a string")
		}
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "minimal" {
			value = "low"
		}
		switch value {
		case "none", "low", "medium", "high", "xhigh", "max":
			opts.ReasoningSet = true
			opts.IsReasoning = value != "none"
			opts.ReasoningEffort = value
		default:
			return opts, invalidRequest("unsupported reasoning effort %q", value)
		}
	}
	if thinking, ok := req["thinking"].(map[string]interface{}); ok && thinking["type"] == "disabled" {
		opts.ReasoningSet = true
		opts.IsReasoning = false
		opts.ReasoningEffort = "none"
	}
	for _, field := range []string{"temperature", "top_p", "tool_choice", "parallel_tool_calls", "stop"} {
		if value, exists := req[field]; exists && value != nil {
			opts.Parameters[field] = value
		}
	}
	return opts, nil
}

package bridge

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"qoder2api/internal/cosy"
)

type Delta struct {
	Role            string
	Content         string
	Reasoning       string
	ToolCalls       []interface{}
	InputTokens     int
	OutputTokens    int
	CachedTokens    int
	ReasoningTokens int
	HasUsage        bool
	FinishReason    string
	Done            bool
	Err             error
}

func (d Delta) hasPayload() bool {
	return d.Role != "" || d.Content != "" || d.Reasoning != "" || d.ToolCalls != nil || d.HasUsage || d.InputTokens != 0 || d.OutputTokens != 0 || d.Err != nil
}

func (d Delta) isEmpty() bool {
	return !d.hasPayload() && d.FinishReason == "" && !d.Done
}

func ExtractDelta(dataLine string) Delta {
	if strings.TrimSpace(dataLine) == "[DONE]" {
		return Delta{Done: true}
	}
	var wrapper map[string]interface{}
	if err := json.Unmarshal([]byte(dataLine), &wrapper); err != nil {
		return Delta{Err: fmt.Errorf("malformed upstream JSON frame")}
	}
	if raw, ok := wrapper["statusCodeValue"]; ok {
		if status := toEnvelopeStatus(raw); status != 200 {
			return Delta{Err: NewUpstreamError(status, envelopeDetail(wrapper))}
		}
	}
	inner := wrapper
	if body, exists := wrapper["body"]; exists {
		switch value := body.(type) {
		case string:
			if strings.TrimSpace(value) == "[DONE]" {
				return Delta{Done: true}
			}
			if value == "" {
				return Delta{}
			}
			inner = nil
			if err := json.Unmarshal([]byte(value), &inner); err != nil {
				return Delta{Err: fmt.Errorf("malformed upstream chunk JSON")}
			}
		case map[string]interface{}:
			inner = value
		default:
			return Delta{Err: fmt.Errorf("invalid upstream chunk body")}
		}
	}
	if code, exists := inner["code"]; exists && code != nil && fmt.Sprint(code) != "" && fmt.Sprint(code) != "0" {
		return Delta{Err: NewStreamBusinessError(fmt.Sprintf("upstream error code=%v: %v", code, inner["message"]))}
	}
	if value, exists := inner["error"]; exists && value != nil {
		return Delta{Err: NewStreamBusinessError(fmt.Sprintf("upstream error: %v", value))}
	}
	var result Delta
	if usage, ok := inner["usage"].(map[string]interface{}); ok {
		result.HasUsage = true
		result.InputTokens = int(cosy.FloatVal(usage, "prompt_tokens"))
		result.OutputTokens = int(cosy.FloatVal(usage, "completion_tokens"))
		if details, ok := usage["prompt_tokens_details"].(map[string]interface{}); ok {
			result.CachedTokens = int(cosy.FloatVal(details, "cached_tokens"))
		}
		if details, ok := usage["completion_tokens_details"].(map[string]interface{}); ok {
			result.ReasoningTokens = int(cosy.FloatVal(details, "reasoning_tokens"))
		}
	}
	choices, _ := inner["choices"].([]interface{})
	for _, choice := range choices {
		entry, _ := choice.(map[string]interface{})
		if reason, ok := entry["finish_reason"].(string); ok && reason != "" {
			result.FinishReason = reason
		}
		fields, _ := entry["delta"].(map[string]interface{})
		if fields == nil {
			continue
		}
		result.Role, _ = fields["role"].(string)
		result.Content, _ = fields["content"].(string)
		result.Reasoning, _ = fields["reasoning_content"].(string)
		result.ToolCalls, _ = fields["tool_calls"].([]interface{})
		break
	}
	return result
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// toEnvelopeStatus 信封 statusCodeValue 可能是 int/float64/str，统一成 int
// （解析失败回 502，按瞬时故障处理）。
func toEnvelopeStatus(v interface{}) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		n, err := strconv.Atoi(x)
		if err != nil {
			return 502
		}
		return n
	}
	return 502
}

// envelopeDetail 提取信封错误详情：body 为字符串时直接用（错误详情在内层），
// 否则截断整帧 JSON 便于排查。
func envelopeDetail(wrapper map[string]interface{}) string {
	if body, ok := wrapper["body"].(string); ok && body != "" {
		return body
	}
	b, _ := json.Marshal(wrapper)
	return truncate(string(b), 400)
}

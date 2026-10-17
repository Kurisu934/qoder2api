package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"qoder2api/internal/cosy"
	"strings"
	"sync"

	"qoder2api/account"
	"qoder2api/logger"
)

func qoderChatStreamURL(region account.Region) string {
	return account.GetEndpoints(region).ChatStreamURL
}

func qoderModelListURL(region account.Region) string {
	return account.GetEndpoints(region).ModelListURL
}

func ParseOAuthSecret(secret string) (deviceToken, refreshToken string) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", ""
	}
	if strings.HasPrefix(secret, "dt-") || strings.HasPrefix(secret, "drt-") {
		return secret, ""
	}
	var payload struct {
		DeviceToken  string `json:"device_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal([]byte(secret), &payload); err != nil {
		return secret, ""
	}
	return payload.DeviceToken, payload.RefreshToken
}

func ResolveOAuthUserID(userInfo map[string]interface{}) string {
	if id := StrVal(userInfo, "id"); id != "" {
		return id
	}
	if id := StrVal(userInfo, "userId"); id != "" {
		return id
	}
	return StrVal(userInfo, "uid")
}

func FetchUserInfoWithToken(token string, region account.Region) (map[string]interface{}, error) {
	ep := account.GetEndpoints(region)
	req, _ := http.NewRequest("GET", ep.UserinfoBase, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	// DEBUG 用途排查原始 userinfo 响应结构：降为 Debug 级并截断至 500 字节，
	// 默认日志级别不再落盘用户名/组织等 PII，保留低级别排查能力
	logger.Debug("userinfo raw (%d bytes): %s", len(raw), truncate(string(raw), 500))
	return result, nil
}

type Bridge struct {
	sess         *cosy.SessionContext
	client       *BearerClient
	region       account.Region
	templateBase map[string]interface{}
	// pool 非空时启用多账号轮询 + 失败换号；为空则退化为单账号（sess/client/region）。
	pool         *Pool
	singleSlotMu sync.Mutex
	singleSlot   *Slot
	// chatStreamURL 测试注入用：非空时覆盖 region 对应的上游 chat 流地址
	chatStreamURL string
}

// QoderModel keeps upstream routing metadata separate from its public display name.
type QoderModel struct {
	Key               string  `json:"-"`
	DisplayName       string  `json:"display_name"`
	Enable            bool    `json:"enable"`
	IsDefault         bool    `json:"is_default"`
	IsReasoning       bool    `json:"default_reasoning"`  // Upstream default mode, not a capability check.
	SupportsReasoning bool    `json:"supports_reasoning"` // Positive evidence; false may mean unknown.
	ContextWindow     int     `json:"context_window,omitempty"`
	MaxOutputTokens   int     `json:"max_output_tokens,omitempty"`
	MaxInputTokens    int     `json:"max_input_tokens,omitempty"`
	PriceFactor       float64 `json:"price_factor,omitempty"`
}

// tokenPrefix 安全截取 token 前 n 字节用于日志展示（不足 n 返回全串）。
// 防止用户误粘贴短 token 时 pat[:10] 之类切片越界 panic；日志只输出前缀不输出全文。
func tokenPrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// NewBridge 创建 API 转换桥接。
// 支持两种认证方式：
//  1. OAuth device token (dt-xxx): 直接使用，调用 /api/v1/userinfo 获取用户信息
//  2. Personal Access Token (PAT): 调用 ExchangeJobToken 转换为 session token
func NewBridge(pat string, region account.Region, templateBase map[string]interface{}) (*Bridge, error) {
	logger.Info("Bridge using token: %s (prefix: %s)", tokenPrefix(pat, 10), tokenPrefix(pat, 4))

	var identity cosy.AuthIdentity
	var name, id string

	deviceToken, refreshToken := ParseOAuthSecret(pat)
	if strings.HasPrefix(deviceToken, "dt-") {
		userInfo, err := FetchUserInfoWithToken(deviceToken, region)
		if err != nil {
			return nil, fmt.Errorf("fetch user info: %w", err)
		}
		name = StrVal(userInfo, "name")
		id = ResolveOAuthUserID(userInfo)
		identity = cosy.AuthIdentity{
			Name:               name,
			Aid:                id,
			Uid:                id,
			OrganizationId:     StrVal(userInfo, "organization_id"),
			OrganizationName:   StrVal(userInfo, "organization_name"),
			UserType:           StrValDefault(userInfo, "userType", "personal_standard"),
			SecurityOauthToken: deviceToken,
			RefreshToken:       refreshToken,
		}
	} else {
		// jobToken 交换发生在拿到 uid 之前：机器头用凭证种子稳定派生
		// （hub 的交换请求不带机器头，此处保留 QCCG 原有行为但消除随机漂移）
		seed := cosy.FingerprintSeed("", pat)
		jt, err := cosy.ExchangeJobToken(pat, cosy.DeriveMachineID(seed), cosy.DeriveMachineToken(seed), cosy.DeriveMachineType(seed), account.GetEndpoints(region).JobTokenURL)
		if err != nil {
			return nil, fmt.Errorf("exchangeJobToken: %w", err)
		}
		name = StrVal(jt, "name")
		id = StrVal(jt, "id")
		identity = cosy.AuthIdentity{
			Name:               name,
			Aid:                id,
			Uid:                id,
			UserType:           StrValDefault(jt, "userType", "personal_standard"),
			SecurityOauthToken: StrVal(jt, "securityOauthToken"),
			RefreshToken:       StrVal(jt, "refreshToken"),
		}
	}

	logger.Info("Bridge session for %s (%s)", name, id)
	// 稳定设备指纹：优先 uid 派生（双区统一、重启不变），uid 缺失退回凭证种子
	seed := cosy.FingerprintSeed(id, pat)
	mid := cosy.DeriveMachineID(seed)
	mtoken := cosy.DeriveMachineToken(seed)
	mtype := cosy.DeriveMachineType(seed)
	logger.Info("Bridge fingerprint machineid=%s (stable derive)", mid)
	sess, err := cosy.NewSession(identity, mid, mtoken, mtype)
	if err != nil {
		return nil, err
	}
	client := NewBearerClient(sess)

	return &Bridge{
		sess:         sess,
		client:       client,
		region:       region,
		templateBase: templateBase,
	}, nil
}

// ListAvailableModels returns only enabled, named, unambiguous live catalog entries.
// The same per-account catalog cache is used for listing and request resolution.
func (b *Bridge) ListAvailableModels() ([]QoderModel, error) {
	models, err := b.catalogModels()
	if len(models) == 0 {
		return nil, err
	}
	return publicModels(models), nil
}

func parseQoderModels(resp map[string]interface{}) []QoderModel {
	// 按优先级尝试多个分类
	categories := []string{"assistant", "developer", "chat"}
	for _, cat := range categories {
		rawList, _ := resp[cat].([]interface{})
		out := extractModels(rawList)
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func extractModels(rawList []interface{}) []QoderModel {
	out := make([]QoderModel, 0, len(rawList))
	for _, it := range rawList {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		enable, _ := m["enable"].(bool)
		isDefault, _ := m["is_default"].(bool)
		isReasoning, _ := m["is_reasoning"].(bool)
		model := QoderModel{
			Key:             strings.TrimSpace(StrVal(m, "key")),
			DisplayName:     strings.TrimSpace(StrVal(m, "display_name")),
			Enable:          enable,
			IsDefault:       isDefault,
			IsReasoning:     isReasoning,
			ContextWindow:   positiveModelInt(m, "context_window", "context_length"),
			MaxInputTokens:  positiveModelInt(m, "max_input_tokens"),
			MaxOutputTokens: positiveModelInt(m, "max_output_tokens"),
			PriceFactor:     cosy.FloatVal(m, "price_factor"),
		}
		model.SupportsReasoning = modelReasoningCapability(m, model)
		if contextConfig := modelContextConfig(m); contextConfig != nil {
			if model.ContextWindow == 0 {
				model.ContextWindow = positiveModelInt(contextConfig, "token_count", "context_window")
			}
			if model.MaxInputTokens == 0 {
				model.MaxInputTokens = positiveModelInt(contextConfig, "max_input_tokens")
			}
			if model.MaxOutputTokens == 0 {
				model.MaxOutputTokens = positiveModelInt(contextConfig, "max_output_tokens")
			}
		}
		// Expose the same usable maximum that request routing sends upstream,
		// rather than a smaller Auto/default context preset.
		if model.MaxInputTokens > 0 {
			model.ContextWindow = model.MaxInputTokens
		}
		out = append(out, model)
	}
	return out
}

// DeepCopyMap does a JSON round-trip deep copy.
func DeepCopyMap(m map[string]interface{}) map[string]interface{} {
	data, _ := json.Marshal(m)
	var out map[string]interface{}
	json.Unmarshal(data, &out)
	return out
}

// CallOpts 控制单次 CallQoder 的可选行为。
type CallOpts struct {
	IsReasoning     bool // Client intent; Qoder model_config.is_reasoning is the model's default type.
	ReasoningSet    bool
	ReasoningEffort string
	MaxTokens       int
	Parameters      map[string]interface{}
	model           QoderModel
}

func (b *Bridge) CallQoder(ctx context.Context, agent string, messages []interface{}, model string, tools interface{}, onDelta func(Delta)) error {
	return b.CallQoderWithOpts(ctx, agent, messages, model, tools, CallOpts{}, onDelta)
}

// deltaDispatcher 把上游 SSE 行分发为 Delta 回调，返回值供 openStreamLines
// 的 onLine 使用：false 表示停止读取上游流。
// 约束（review P1）：一旦捕获到流内业务错误（*upstreamErr 非 nil），
// 立即停止向 onDelta 分发任何后续 delta，且从置位错误的那一行起返回 false，
// 让 openStreamLines 及时退出——上游发出错误帧后若不关流，否则重开闸门
// 永不触发、请求挂起；错误由 CallQoder 在流结束后统一返回。
func deltaDispatcher(onDelta func(Delta), upstreamErr *error) func(string) bool {
	finished := false
	finishEvent := false
	return func(line string) bool {
		if *upstreamErr != nil || finished {
			return false
		}
		var delta Delta
		if strings.HasPrefix(line, "event:") {
			finishEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:")) == "finish"
			return true
		} else if line == "" && finishEvent {
			// A finish header alone is not a dispatched SSE event. Parse its data
			// first, including any error/usage, and wait for the event boundary.
			delta.Done = true
			finishEvent = false
		} else if strings.HasPrefix(line, "data:") {
			delta = ExtractDelta(strings.TrimSpace(line[5:]))
		} else {
			return true
		}
		if delta.Err != nil {
			*upstreamErr = delta.Err
			return false
		}
		if !delta.isEmpty() {
			onDelta(delta)
		}
		finished = delta.Done
		return !finished
	}
}

func (b *Bridge) CallQoderWithOpts(ctx context.Context, agent string, messages []interface{}, model string, tools interface{}, opts CallOpts, onDelta func(Delta)) error {
	selected := opts.model
	if selected.Key == "" {
		var err error
		selected, err = b.resolveModel(agent, model)
		if err != nil {
			return err
		}
	}
	model = selected.Key
	body := DeepCopyMap(b.templateBase)

	body["stream"] = true

	mc, ok := body["model_config"].(map[string]interface{})
	if !ok {
		mc = make(map[string]interface{})
		body["model_config"] = mc
	}
	mc["key"] = selected.Key
	mc["display_name"] = selected.DisplayName
	// This flag describes the SKU, not whether a hybrid model should think.
	// Kimi-K3's observed working wire uses false together with reasoning_effort.
	mc["is_reasoning"] = selected.IsReasoning
	params, ok := body["parameters"].(map[string]interface{})
	if !ok {
		params = make(map[string]interface{})
		body["parameters"] = params
	}
	for key, value := range opts.Parameters {
		params[key] = value
	}
	if opts.MaxTokens > 0 {
		params["max_tokens"] = opts.MaxTokens
	}
	capacity := selected.MaxInputTokens
	if capacity == 0 {
		capacity = selected.ContextWindow
	}
	if capacity > 0 {
		mc["max_input_tokens"] = capacity
		params["context_length"] = capacity
	}
	if opts.ReasoningEffort != "" {
		params["reasoning_effort"] = opts.ReasoningEffort
	} else if opts.ReasoningSet || opts.IsReasoning {
		if opts.IsReasoning {
			params["reasoning_effort"] = "high"
		} else {
			params["reasoning_effort"] = "none"
		}
	}

	// Extract summary prompt from last user message for chat_context and business.name
	prompt := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if mm, ok := messages[i].(map[string]interface{}); ok && mm["role"] == "user" {
			prompt = NormalizeMessageContent(mm)
			if prompt == "" {
				if contents, ok := mm["contents"].([]interface{}); ok {
					for _, block := range contents {
						if b, ok := block.(map[string]interface{}); ok {
							if t, ok := b["text"].(string); ok {
								prompt = t
								break
							}
						}
					}
				}
			}
			break
		}
	}

	if biz, ok := body["business"].(map[string]interface{}); ok {
		biz["id"] = cosy.NewUUID()
		biz["begin_at"] = cosy.UnixMs()
		if len(prompt) > 30 {
			biz["name"] = prompt[:30]
		} else {
			biz["name"] = prompt
		}
	}

	if cc, ok := body["chat_context"].(map[string]interface{}); ok {
		if txt, ok := cc["text"].(map[string]interface{}); ok {
			txt["text"] = prompt
		}
		if extra, ok := cc["extra"].(map[string]interface{}); ok {
			if oc, ok := extra["originalContent"].(map[string]interface{}); ok {
				oc["text"] = prompt
			}
		}
	}

	body["messages"] = messages
	if tools != nil {
		body["tools"] = tools
	} else {
		delete(body, "tools")
	}

	mcSource := "system"
	if mc, ok := body["model_config"].(map[string]interface{}); ok {
		if s, ok := mc["source"].(string); ok {
			mcSource = s
		}
	}

	extra := map[string]string{
		"x-model-key":    model,
		"x-model-source": mcSource,
	}

	preview := prompt
	if len(prompt) > 80 {
		preview = preview[:80] + "..."
	}
	logger.Info("callQoder region=%s model=%s prompt=%s", b.region, model, preview)

	// ---- 账号池选号 + 失败换号 -------------------------------------------
	// 逐个尝试池内账号：某账号失败且「尚未向客户端发出任何内容」、且错误属于
	// 可换号类（额度/频控/瞬时），就冷却该账号并换下一个；已发出内容或
	// 客户端参数错/内容审核则立即上抛——避免重复内容、避免无意义换号。
	tried := make(map[string]struct{})
	var lastErr error
	for {
		slot, ok := b.pickSlotForModel(model, tried)
		if !ok {
			break
		}
		// 记录本次实际使用的账号（含 region），便于对账与排查
		// 「请求是否路由到了未启动的那一侧账号」类问题。
		logger.Info("callQoder: using account=%s id=%s region=%s model=%s",
			slot.Name, slot.ID, slot.Region, model)
		// 每个账号用全新的请求 id / session，避免跨账号复用同一标识
		nid := cosy.NewUUID()
		body["request_id"] = nid
		body["chat_record_id"] = nid
		body["request_set_id"] = cosy.NewUUID()
		body["session_id"] = cosy.NewUUID()
		body["aliyun_user_type"] = slot.sess.Identity.UserType
		if biz, ok := body["business"].(map[string]interface{}); ok {
			biz["id"] = cosy.NewUUID()
			biz["begin_at"] = cosy.UnixMs()
		}

		qurl := b.chatStreamURL
		if qurl == "" {
			qurl = qoderChatStreamURL(slot.Region)
		}
		logger.Debug("callQoder request body: %s", func() string { d, _ := json.Marshal(body); return string(d) }())

		emitted, err := b.streamWithSlot(ctx, slot, qurl, body, extra, onDelta)
		if err == nil {
			b.noteSlotSuccess(slot.ID)
			return nil
		}
		lastErr = err
		if emitted || !ShouldFailover(err) {
			return err
		}
		b.coolSlot(slot.ID, CooldownFor(err), err.Error())
		tried[slot.ID] = struct{}{}
		logger.Info("callQoder: account=%s id=%s region=%s failed, failing over to next account: %v",
			slot.Name, slot.ID, slot.Region, err)
	}
	if lastErr == nil {
		lastErr = ErrNoAccount
	}
	return lastErr
}

// streamWithSlot 在单个账号上执行「SSE 信封重试闸门」。
// 连接阶段错误已由 openStreamLines 内部重试；此处是第二层防护（有界：
// 最多 TransientMaxRetries 次），主要覆盖上游 HTTP200 建流后才在信封里
// 投递 418/5xx 的形态。返回是否已向 onDelta 发出过内容——上层据此决定
// 能否换号（已发出内容则不能，否则客户端会收到重复内容）。
func (b *Bridge) streamWithSlot(ctx context.Context, slot *Slot, qurl string, body map[string]interface{}, extra map[string]string, onDelta func(Delta)) (bool, error) {
	var lastErr error
	emittedAny := false
	for attempt := 0; attempt <= TransientMaxRetries; attempt++ {
		if attempt > 0 {
			if serr := sleepCtx(ctx, RetryBackoff(attempt)); serr != nil {
				return emittedAny, lastErr
			}
			logger.Info("reopen upstream after in-stream error (try %d/%d): %v",
				attempt+1, TransientMaxRetries+1, lastErr)
		}
		emitted := false
		var upstreamErr error
		onDeltaWrapped := func(d Delta) {
			if d.hasPayload() {
				emitted = true
				emittedAny = true
			}
			onDelta(d)
		}
		streamErr := slot.client.openStreamLines(ctx, qurl, body, extra,
			deltaDispatcher(onDeltaWrapped, &upstreamErr))
		if upstreamErr == nil && streamErr == nil {
			// 上游正常关流但未发出任何有效 delta/usage 帧：对齐 hub
			// "empty upstream stream" 显式报错（不纳入重开闸门）。
			if !emitted {
				return emittedAny, ErrEmptyStream
			}
			return emittedAny, nil
		}
		if upstreamErr != nil {
			lastErr = upstreamErr
		} else {
			lastErr = streamErr
		}
		if emitted || attempt >= TransientMaxRetries || !isRetryableStreamError(lastErr) {
			return emittedAny, lastErr
		}
	}
	return emittedAny, lastErr
}

// isRetryableStreamError 判定流内/连接错误是否值得重开上游：
// 结构化上游错误按瞬时分类判定；裸传输错误（TLS EOF 等）按传输层判定；
// ctx 取消、业务错误、内容审核等一律不可重试。
func isRetryableStreamError(err error) bool {
	if err == nil {
		return false
	}
	var ue *UpstreamError
	if errors.As(err, &ue) {
		return IsTransientUpstream(ue.Status, ue.Detail)
	}
	return IsTransientTransport(err)
}

// RedactRequestBodyJSON 接收原始请求 JSON 字节，深拷贝后把可能含敏感对话内容的字段
// （messages[*].content / system / input / tools[*].description 等）的字符串值替换为
// `<redacted len=N>`，保留 JSON 结构和长度信息便于排查问题，但不泄露用户对话原文。
//
// 设计：
//  1. 只对 string 类型脱敏，结构和数字保留
//  2. 长度阈值：>32 才脱敏（避免把短角色名 "user" 也替换掉）
//  3. 按字段名递归：content/text/system/input/instructions/prompt/description
//  4. 失败时退回原始内容（脱敏只是日志辅助，不能影响主流程）
func RedactRequestBodyJSON(raw []byte) string {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	redactValue(v, "")
	out, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// sensitiveFieldNames 命中后其字符串子值会被脱敏（递归进数组/对象继续处理）
var sensitiveFieldNames = map[string]bool{
	"content":      true,
	"text":         true,
	"system":       true,
	"input":        true,
	"instructions": true,
	"prompt":       true,
	"description":  true,
}

func redactValue(v interface{}, parentField string) {
	switch x := v.(type) {
	case map[string]interface{}:
		for k, child := range x {
			if s, ok := child.(string); ok {
				if (sensitiveFieldNames[k] || sensitiveFieldNames[parentField]) && len(s) > 32 {
					x[k] = fmt.Sprintf("<redacted len=%d>", len(s))
				}
				continue
			}
			redactValue(child, k)
		}
	case []interface{}:
		for i, item := range x {
			if s, ok := item.(string); ok {
				if sensitiveFieldNames[parentField] && len(s) > 32 {
					x[i] = fmt.Sprintf("<redacted len=%d>", len(s))
				}
				continue
			}
			redactValue(item, parentField)
		}
	}
}

func StrVal(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func StrValDefault(m map[string]interface{}, key, def string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return def
}

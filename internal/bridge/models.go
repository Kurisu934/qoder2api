package bridge

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"qoder2api/account"
	"qoder2api/internal/cosy"
)

// modelRegion keeps listing, name resolution and account selection in the same
// region, including bridges built without an explicit region.
func (b *Bridge) modelRegion() account.Region {
	if b.region != "" {
		return b.region
	}
	if b.pool != nil {
		slots := b.pool.snapshot()
		if len(slots) > 0 {
			return slots[0].Region
		}
	}
	return ""
}

// catalogModels retains disabled and unnamed records internally so resolution
// can distinguish an unavailable name from one that never existed. Partial
// catalog failures are returned alongside the records from working accounts.
func (b *Bridge) catalogModels() ([]QoderModel, error) {
	wantRegion := b.modelRegion()
	var models []QoderModel
	var catalogErr error
	available := false
	now := time.Now()
	for _, slot := range b.candidateSlots() {
		if (wantRegion != "" && slot.Region != wantRegion) || !slot.available(now) {
			continue
		}
		available = true
		if err := b.ensureCatalog(slot); err != nil {
			catalogErr = fmt.Errorf("load model catalog: %w", err)
			continue
		}
		slot.catalogMu.Lock()
		models = append(models, slot.catalog...)
		slot.catalogMu.Unlock()
	}
	if !available {
		return nil, ErrNoAccount
	}
	if len(models) == 0 && catalogErr == nil {
		catalogErr = fmt.Errorf("upstream model catalog contains no models")
	}
	return models, catalogErr
}

func publicModel(model QoderModel) bool {
	return model.Enable && model.Key != "" && model.DisplayName != ""
}

func publicModels(models []QoderModel) []QoderModel {
	// Case-insensitive collisions with different internal SKUs are not valid
	// selectable public IDs. Repeated records for the same SKU are harmless.
	type entry struct {
		model     QoderModel
		ambiguous bool
	}
	names := make(map[string]entry, len(models))
	for _, model := range models {
		if !publicModel(model) {
			continue
		}
		name := strings.ToLower(model.DisplayName)
		previous, exists := names[name]
		if !exists {
			names[name] = entry{model: model}
			continue
		}
		if previous.model.Key != model.Key {
			previous.ambiguous = true
		}
		// Account order and Go map iteration never decide the displayed spelling.
		if model.DisplayName < previous.model.DisplayName {
			previous.model = model
		}
		names[name] = previous
	}
	out := make([]QoderModel, 0, len(names))
	for _, item := range names {
		if !item.ambiguous {
			out = append(out, item.model)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDefault != out[j].IsDefault {
			return out[i].IsDefault
		}
		left, right := strings.ToLower(out[i].DisplayName), strings.ToLower(out[j].DisplayName)
		if left != right {
			return left < right
		}
		return out[i].DisplayName < out[j].DisplayName
	})
	return out
}

func invalidModel(format string, args ...interface{}) error {
	message := fmt.Sprintf(format, args...)
	return &UpstreamError{Status: 400, Detail: message, ErrType: "invalid_request_error", Message: message}
}

// configuredModel accepts explicit aliases only. Neither family substrings nor
// arbitrary lowercased names are treated as upstream SKUs.
func configuredModel(agent, requested string) (string, bool, error) {
	settings, err := account.LoadSettings()
	if err != nil {
		return "", false, fmt.Errorf("load model mappings: %w", err)
	}
	if settings == nil {
		return requested, false, nil
	}
	var target string
	matched := false
	for source, destination := range settings.ModelMappings[agent] {
		if !strings.EqualFold(strings.TrimSpace(source), requested) {
			continue
		}
		destination = strings.TrimSpace(destination)
		if matched && !strings.EqualFold(target, destination) {
			return "", false, invalidModel("model alias %q is ambiguous", requested)
		}
		if !matched || destination < target {
			target = destination
		}
		matched = true
	}
	if matched {
		if target == "" {
			return "", false, invalidModel("model alias %q has an empty target", requested)
		}
		return target, true, nil
	}
	return requested, false, nil
}

// resolveModel is the only public-name-to-wire-key boundary. Internal keys are
// accepted as targets of explicit user mappings, never as implicit public IDs.
func (b *Bridge) resolveModel(agent, requested string) (QoderModel, error) {
	requested = strings.TrimSpace(requested)
	var target string
	var mapped bool
	var err error
	if requested == "" {
		requested, target = "Auto", "Auto"
	} else {
		target, mapped, err = configuredModel(agent, requested)
		if err != nil {
			return QoderModel{}, err
		}
	}
	models, catalogErr := b.catalogModels()
	var resolved QoderModel
	found, unavailable := false, false
	for _, model := range models {
		nameMatches := strings.EqualFold(model.DisplayName, target)
		keyMatches := mapped && strings.EqualFold(model.Key, target)
		if !nameMatches && !keyMatches {
			continue
		}
		if !publicModel(model) {
			unavailable = true
			continue
		}
		if found && (resolved.Key != model.Key || !strings.EqualFold(resolved.DisplayName, model.DisplayName)) {
			return QoderModel{}, invalidModel("model %q is ambiguous in the upstream catalog", requested)
		}
		if !found || model.DisplayName < resolved.DisplayName {
			resolved = model
		}
		found = true
	}
	if found {
		return resolved, nil
	}
	if catalogErr != nil {
		return QoderModel{}, catalogErr
	}
	if unavailable {
		return QoderModel{}, invalidModel("model %q is disabled or has no public display name", requested)
	}
	return QoderModel{}, invalidModel("unknown model %q; use a display name from /v1/models", requested)
}

func positiveModelInt(values map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		if value := int(cosy.FloatVal(values, key)); value > 0 {
			return value
		}
	}
	return 0
}

func modelReasoningCapability(raw map[string]interface{}, model QoderModel) bool {
	if model.IsReasoning {
		return true
	}
	for _, key := range []string{"supports_reasoning", "support_reasoning", "supports_thinking"} {
		if supported, _ := raw[key].(bool); supported {
			return true
		}
	}
	if capabilities, ok := raw["capabilities"].(map[string]interface{}); ok {
		for _, key := range []string{"reasoning", "thinking"} {
			if supported, _ := capabilities[key].(bool); supported {
				return true
			}
		}
	}
	// Kimi-K3 has observed upstream reasoning support; is_reasoning=false in
	// its current catalog denotes the default mode, not a missing capability.
	return model.Key == "kmodel_latest" && strings.EqualFold(model.DisplayName, "Kimi-K3")
}

func modelContextConfig(raw map[string]interface{}) map[string]interface{} {
	configs, ok := raw["context_config"].(map[string]interface{})
	if !ok {
		return nil
	}
	if enabled, present := configs["enable"].(bool); present && !enabled {
		return nil
	}
	if positiveModelInt(configs, "token_count", "context_window", "max_input_tokens") > 0 {
		return configs
	}
	var selected map[string]interface{}
	largest := 0
	selectedKey := ""
	for key, rawConfig := range configs {
		config, _ := rawConfig.(map[string]interface{})
		if enabled, present := config["enable"].(bool); present && !enabled {
			continue
		}
		capacity := positiveModelInt(config, "token_count", "context_window", "max_input_tokens")
		if capacity > largest || capacity > 0 && capacity == largest && key < selectedKey {
			selected, largest, selectedKey = config, capacity, key
		}
	}
	return selected
}

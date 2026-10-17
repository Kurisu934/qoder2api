package bridge

import (
	"fmt"
	"time"

	"qoder2api/account"
)

// NewSlotFromSecret 用账号凭证构建一个可入池的 Slot（复用 NewBridge 的
// 会话构建逻辑：OAuth 直接 userinfo，PAT 走 jobToken 交换 + 稳定指纹）。
func NewSlotFromSecret(id, name string, region account.Region, secret string) (*Slot, error) {
	b, err := NewBridge(secret, region, nil)
	if err != nil {
		return nil, err
	}
	return NewSlot(id, name, region, b.sess, b.client), nil
}

// NewPoolBridge 用账号池创建桥接（多账号轮询 + 失败换号）。
func NewPoolBridge(pool *Pool, templateBase map[string]interface{}) *Bridge {
	return &Bridge{pool: pool, templateBase: templateBase}
}

// NewPoolBridgeFor 指定本桥 region，用于选号时拒绝跨区账号。
func NewPoolBridgeFor(region account.Region, pool *Pool, templateBase map[string]interface{}) *Bridge {
	return &Bridge{pool: pool, templateBase: templateBase, region: region}
}

// candidateSlots 返回当前可参与选号的账号：池优先；无池时退化为单账号
// （保持既有单账号构造方式与测试兼容）。
func (b *Bridge) candidateSlots() []*Slot {
	if b.pool != nil {
		return b.pool.snapshot()
	}
	if b.sess == nil || b.client == nil {
		return nil
	}
	b.singleSlotMu.Lock()
	defer b.singleSlotMu.Unlock()
	if b.singleSlot == nil {
		b.singleSlot = NewSlot("default", "default", b.region, b.sess, b.client)
	}
	return []*Slot{b.singleSlot}
}

// pickSlot 选一个账号，跳过 exclude 与冷却中的账号。
func (b *Bridge) pickSlot(exclude map[string]struct{}) (*Slot, bool) {
	if b.pool != nil {
		return b.pool.Pick(exclude)
	}
	slots := b.candidateSlots()
	if len(slots) == 0 {
		return nil, false
	}
	if _, skip := exclude[slots[0].ID]; skip {
		return nil, false
	}
	return slots[0], true
}

// coolSlot 冷却账号（仅池模式下生效；单账号模式为 no-op）。
func (b *Bridge) coolSlot(id string, d time.Duration, reason string) {
	if b.pool != nil {
		b.pool.Cool(id, d, reason)
	}
}

// noteSlotSuccess 记录成功（仅池模式下生效）。
func (b *Bridge) noteSlotSuccess(id string) {
	if b.pool != nil {
		b.pool.NoteSuccess(id)
	}
}

// Catalog caches are scoped to an account and its region. A failed refresh is
// cached for the existing TTL and never replaced by fabricated model entries.
const modelCatalogTTL = 30 * time.Minute

func (b *Bridge) ensureCatalog(slot *Slot) error {
	slot.catalogMu.Lock()
	defer slot.catalogMu.Unlock()
	if !slot.catalogAt.IsZero() && time.Since(slot.catalogAt) < modelCatalogTTL {
		return slot.catalogErr
	}
	// Serialize refreshes on this account so concurrent inference/list calls share
	// the same request rather than starting duplicate signed catalog fetches.
	models, err := b.fetchSlotModels(slot)
	slot.catalogAt = time.Now()
	slot.catalogErr = err
	if err == nil {
		slot.catalog = models
	}
	return err
}

func (b *Bridge) fetchSlotModels(slot *Slot) ([]QoderModel, error) {
	resp, err := slot.client.callGet(qoderModelListURL(slot.Region))
	if err != nil {
		return nil, err
	}
	models := parseQoderModels(resp)
	if len(models) == 0 {
		return nil, fmt.Errorf("empty upstream model catalog for region %s", slot.Region)
	}
	return models, nil
}

// slotSupportsModel 该账号能否提供此模型；目录未知时返回 true（不过滤）。
func (b *Bridge) slotSupportsModel(slot *Slot, model string) bool {
	slot.catalogMu.Lock()
	defer slot.catalogMu.Unlock()
	if slot.catalog == nil {
		return true
	}
	for _, candidate := range slot.catalog {
		if candidate.Key == model && candidate.Enable && candidate.DisplayName != "" {
			return true
		}
	}
	return false
}

// pickSlotForModel selects only same-region accounts whose known catalog allows
// this exact SKU. Never fall back to an account known not to support the model.
func (b *Bridge) pickSlotForModel(model string, exclude map[string]struct{}) (*Slot, bool) {
	wantRegion := b.modelRegion()
	// 防御性过滤：即使池被误构建为跨区，也不选别区账号
	// （跨区会扣到用户根本没启动的那侧账号的 credits）。
	sameRegion := func(s *Slot) bool { return wantRegion == "" || s.Region == wantRegion }

	if b.pool == nil {
		return b.pickSlot(exclude)
	}
	return b.pool.Pick(exclude, func(s *Slot) bool {
		if !sameRegion(s) {
			return false
		}
		b.ensureCatalog(s)
		return b.slotSupportsModel(s, model)
	})
}

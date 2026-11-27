package gemini

import (
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
)

// DefaultModelID 是解析为各账号官方默认模型的公开 ID
const DefaultModelID = "gemini-default"

// CatalogEntry 是账号目录中一个位置键上的公开 ID，Alternate 是该位置在两个名称间交替时的另一个 ID
type CatalogEntry struct {
	Position  string `json:"position"`
	ID        string `json:"id"`
	Alternate string `json:"alternate,omitempty"`
}

// AccountCatalog 是账号最近一次读取的目录：层级、各位置键上的公开 ID 与上锁的公开 ID
type AccountCatalog struct {
	Tier    string         `json:"tier"`
	Entries []CatalogEntry `json:"entries"`
	Locked  []string       `json:"locked,omitempty"`
}

// ModelNameStore 保存公开 ID 的改名记录与各账号最近一次读取的目录
type ModelNameStore interface {
	LoadModelNames() (renames map[string]string, catalogs map[string]AccountCatalog, err error)
	SaveModelNames(accountID string, catalog AccountCatalog, renames map[string]string) error
}

// modelNames 是进程内的改名记录（旧 ID 到当前 ID）与各账号最近一次读取的目录
var modelNames struct {
	sync.RWMutex
	renames  map[string]string
	catalogs map[string]AccountCatalog
	store    ModelNameStore
}

// LoadModelNames 从 store 读入改名记录与各账号最近的目录，之后读到的目录写回 store
func LoadModelNames(store ModelNameStore) error {
	renames, catalogs, err := store.LoadModelNames()
	if err != nil {
		return err
	}
	modelNames.Lock()
	defer modelNames.Unlock()
	modelNames.renames, modelNames.catalogs, modelNames.store = renames, catalogs, store
	return nil
}

// NormalizeModelID 返回去掉首尾空白、Gemini API 的 models/ 前缀、社区通用的 google/ 前缀与 Claude Code 1M 上下文模式的 [1m] 后缀并转为小写的模型 ID
func NormalizeModelID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, "[1m]")
	value = strings.TrimPrefix(value, "models/")
	return strings.TrimPrefix(value, "google/")
}

// CanonicalModelID 返回规范化后的模型 ID，有改名记录时为改名后的当前 ID
func CanonicalModelID(value string) string {
	id := NormalizeModelID(value)
	if current, ok := renamedModel(id); ok {
		return current
	}
	return id
}

// renamedModel 返回旧 ID 改名后的当前 ID
func renamedModel(id string) (string, bool) {
	modelNames.RLock()
	defer modelNames.RUnlock()
	current, ok := modelNames.renames[id]
	return current, ok
}

// alternateModel 返回账号最近一次读取的目录中与 id 交替出现在同一位置上的当前 ID
func alternateModel(accountID, id string) (string, bool) {
	id = NormalizeModelID(id)
	modelNames.RLock()
	defer modelNames.RUnlock()
	for _, entry := range modelNames.catalogs[accountID].Entries {
		if entry.Alternate == id {
			return entry.ID, true
		}
	}
	return "", false
}

// ModelRenames 返回改名记录的副本，键为旧 ID，值为当前 ID
func ModelRenames() map[string]string {
	modelNames.RLock()
	defer modelNames.RUnlock()
	return maps.Clone(modelNames.renames)
}

// AccountCatalogs 返回各账号最近一次读取的目录副本
func AccountCatalogs() map[string]AccountCatalog {
	modelNames.RLock()
	defer modelNames.RUnlock()
	return maps.Clone(modelNames.catalogs)
}

// ObserveCatalog 把账号当前的目录与层级交给 observeCatalog，尚未初始化时不做任何事
func (c *Client) ObserveCatalog() {
	bootstrap, err := c.bootstrapSnapshot()
	if err != nil {
		return
	}
	observeCatalog(c.accountID, c.tierFor(bootstrap), bootstrap.Catalog)
}

// observeCatalog 记录账号读到的目录：与该账号上次同层级的目录比较，同一位置键上的 ID 由 A 变为 B、A 不在新目录且 B 不在旧目录时记一条改名，已有指向 A 的记录改指 B；B 已记为改名到 A 时是目录在两个名称间交替，不记改名，把 A 记为该位置的交替 ID；再保存本次目录
func observeCatalog(accountID, tier string, catalog ModelCatalog) {
	current := AccountCatalog{Tier: tier, Locked: slices.Clone(catalog.locked)}
	ids := make(map[string]bool)
	for _, model := range catalog.models {
		if model.Position == "" || model.Gem != "" {
			continue
		}
		id := strings.ToLower(model.ID)
		current.Entries = append(current.Entries, CatalogEntry{Position: model.Position, ID: id})
		ids[id] = true
	}
	modelNames.Lock()
	defer modelNames.Unlock()
	if modelNames.renames == nil {
		modelNames.renames = make(map[string]string)
	}
	if modelNames.catalogs == nil {
		modelNames.catalogs = make(map[string]AccountCatalog)
	}
	previous, seen := modelNames.catalogs[accountID]
	if seen && previous.Tier == tier {
		previousIDs := make(map[string]bool)
		for _, entry := range previous.Entries {
			previousIDs[entry.ID] = true
		}
		for _, entry := range previous.Entries {
			index := slices.IndexFunc(current.Entries, func(next CatalogEntry) bool { return next.Position == entry.Position })
			if index < 0 {
				continue
			}
			next := current.Entries[index].ID
			switch {
			case next == entry.ID:
				current.Entries[index].Alternate = entry.Alternate
				continue
			case ids[entry.ID] || previousIDs[next] || modelNames.renames[entry.ID] == next:
				continue
			case modelNames.renames[next] == entry.ID:
				current.Entries[index].Alternate = entry.ID
				continue
			}
			for old, target := range modelNames.renames {
				if target == entry.ID {
					modelNames.renames[old] = next
				}
			}
			modelNames.renames[entry.ID] = next
			delete(modelNames.renames, next)
			logging.Info(accountID, "模型改名", "旧 ID", entry.ID, "新 ID", next)
		}
	}
	modelNames.catalogs[accountID] = current
	if modelNames.store != nil {
		if err := modelNames.store.SaveModelNames(accountID, current, modelNames.renames); err != nil {
			logging.Warn(accountID, "保存模型目录与改名记录失败", "错误", err)
		}
	}
}

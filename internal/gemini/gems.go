package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GemModelPrefix 是 Gem 在公开模型目录中的 ID 前缀
const GemModelPrefix = "gem-"

// listGemsRPC 是官网读取 Gem 列表的 batchexecute RPC
const listGemsRPC = "CNgdBe"

// Gem 是官网的一个 Gem：系统预置或账号自建
type Gem struct {
	ID          string
	Name        string
	Description string
	Predefined  bool
}

// fetchGems 按官网页面加载的顺序读取 Gem 列表，列表类型 2 为账号自建，1 为账号可用的其他 Gem，3 为 Gems 管理页的 Google 预置 Gem
func (c *Client) fetchGems(ctx context.Context, bootstrap Bootstrap) ([]Gem, error) {
	var gems []Gem
	for _, kind := range []struct {
		value      int
		predefined bool
	}{{2, false}, {1, true}, {3, true}} {
		request, err := json.Marshal([]any{kind.value, []any{bootstrap.Language}, 0})
		if err != nil {
			return nil, err
		}
		payload, err := c.batchExecute(ctx, bootstrap, batchRequest{
			rpcID: listGemsRPC, payload: string(request), sourcePath: "/app", language: bootstrap.Language, extension: "[0]",
		})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", listGemsRPC, err)
		}
		gems = append(gems, parseGems(payload, kind.predefined)...)
	}
	return gems, nil
}

// parseGems 解析 CNgdBe 载荷第 3 项的 Gem 行：[ID, [名称, 说明], [指令]]
func parseGems(payload []any, predefined bool) []Gem {
	rows, _ := arrayAt(payload, 2)
	gems := make([]Gem, 0, len(rows))
	for _, raw := range rows {
		row, _ := raw.([]any)
		id, _ := stringAt(row, 0)
		meta, _ := arrayAt(row, 1)
		name, _ := stringAt(meta, 0)
		if strings.TrimSpace(id) == "" || strings.TrimSpace(name) == "" {
			continue
		}
		description, _ := stringAt(meta, 1)
		gems = append(gems, Gem{ID: id, Name: name, Description: description, Predefined: predefined})
	}
	return gems
}

// addGems 以账号默认模型为底把 Gem 加入目录，公开 ID 为 gem-<Gem ID>，同一 Gem 出现在多个列表时只加入第一次
func (c *ModelCatalog) addGems(gems []Gem) {
	if c.defaultIndex < 0 || c.defaultIndex >= len(c.models) {
		return
	}
	base := c.models[c.defaultIndex]
	for _, gem := range gems {
		if _, exists := c.lookup[strings.ToLower(GemModelPrefix+gem.ID)]; exists {
			continue
		}
		entry := base
		entry.ID, entry.DisplayName, entry.Description, entry.Default, entry.Gem = GemModelPrefix+gem.ID, gem.Name, gem.Description, false, gem.ID
		c.add(entry)
	}
}

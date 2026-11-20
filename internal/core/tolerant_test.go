package core

import (
	"fmt"
	"strings"
	"testing"
)

func TestTolerantLoadKeepsGoodFields(t *testing.T) {
	good := `{
		"active_provider": 0,
		"model": "deepseek-chat",
		"providers": [
			{"id":"deepseek","name":"DeepSeek","api_key":"sk-aaa","base_url":"https://api.deepseek.com"},
			{"id":"with-bad-field","name":42,"api_key":"sk-bbb","base_url":"https://x.test/v1"}
		]
	}`
	c, bad, err := tolerantLoad([]byte(good))
	if err != nil {
		t.Fatalf("不应失败：%v", err)
	}
	// 顶层好字段保留
	if c.Model != "deepseek-chat" {
		t.Fatalf("好字段被丢：Model=%q", c.Model)
	}
	// 两个 provider 都必须在（坏字段不拖垮整个 provider）
	if len(c.Providers) != 2 {
		t.Fatalf("provider 应为 2 个，实际 %d", len(c.Providers))
	}
	byID := map[string]Provider{}
	for _, p := range c.Providers {
		byID[p.ID] = p
	}
	if byID["deepseek"].APIKey != "sk-aaa" {
		t.Fatal("好 provider 的 Key 丢了")
	}
	// 坏字段所在 provider 的 API Key 必须还在（这是本次改造的全部意义）
	if byID["with-bad-field"].APIKey != "sk-bbb" {
		t.Fatal("坏字段所在 provider 的 API Key 丢了——这正是本次改造要解决的")
	}
	// badKeys 里必须点出 providers[1].name
	joined := strings.Join(bad, ",")
	if !strings.Contains(joined, "providers[1].name") {
		t.Fatalf("badKeys 应含 providers[1].name，实际 %v", bad)
	}
}

func TestTolerantLoadKeepsSiblingsOnLeafError(t *testing.T) {
	good := fmt.Sprintf(`{
		"active_provider": 0,
		"model": "deepseek-chat",
		"theme": "dark",
		"budget_tokens": "not-a-number"
	}`)
	c, bad, err := tolerantLoad([]byte(good))
	if err != nil {
		t.Fatalf("不应失败：%v", err)
	}
	if c.Theme != "dark" {
		t.Fatalf("好字段 theme 被丢：%q", c.Theme)
	}
	if !strings.Contains(strings.Join(bad, ","), "budget_tokens") {
		t.Fatalf("badKeys 应含 budget_tokens，实际 %v", bad)
	}
}

func TestTolerantLoadTopLevelNotObject(t *testing.T) {
	_, _, err := tolerantLoad([]byte(`[1,2,3]`))
	if err == nil {
		t.Fatal("顶层不是对象时必须返回错误")
	}
}

func TestMergeConfigStillPromotesPresets(t *testing.T) {
	// 空 providers 应被默认内置端点顶上（原 mergeConfig 语义不能丢）
	saved := &Config{Providers: nil, Model: ""}
	merged := mergeConfig(DefaultConfig(), saved)
	if len(merged.Providers) == 0 {
		t.Fatal("空 providers 应并回内置预设")
	}
}

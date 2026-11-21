package llm

import (
	"encoding/json"
	"testing"
)

// 各家缓存字段都要能解析成统一的 CachedTokens。
func TestUsageCacheFields(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		cached int
		write  int
		prompt int
		total  int
	}{
		{
			name:   "openai-details",
			body:   `{"prompt_tokens":2000,"completion_tokens":100,"total_tokens":2100,"prompt_tokens_details":{"cached_tokens":1536}}`,
			cached: 1536, prompt: 2000, total: 2100,
		},
		{
			name:   "deepseek",
			body:   `{"prompt_tokens":1000,"completion_tokens":50,"total_tokens":1050,"prompt_cache_hit_tokens":768,"prompt_cache_miss_tokens":232}`,
			cached: 768, prompt: 1000, total: 1050,
		},
		{
			name:   "anthropic",
			body:   `{"prompt_tokens":900,"completion_tokens":20,"cache_read_input_tokens":800,"cache_creation_input_tokens":64}`,
			cached: 800, write: 64, prompt: 900, total: 920,
		},
		{
			name:   "no-cache-and-no-total",
			body:   `{"prompt_tokens":300,"completion_tokens":40}`,
			cached: 0, prompt: 300, total: 340,
		},
	}
	for _, c := range cases {
		var u Usage
		if err := json.Unmarshal([]byte(c.body), &u); err != nil {
			t.Fatalf("%s: 解析失败 %v", c.name, err)
		}
		if u.CachedTokens != c.cached {
			t.Fatalf("%s: 命中量 = %d，期望 %d", c.name, u.CachedTokens, c.cached)
		}
		if u.CacheWriteTokens != c.write {
			t.Fatalf("%s: 写入量 = %d，期望 %d", c.name, u.CacheWriteTokens, c.write)
		}
		if u.PromptTokens != c.prompt || u.TotalTokens != c.total {
			t.Fatalf("%s: prompt/total = %d/%d，期望 %d/%d",
				c.name, u.PromptTokens, u.TotalTokens, c.prompt, c.total)
		}
	}
}

// 命中量不会被重复相加。
func TestUsageCacheNoDoubleCount(t *testing.T) {
	body := `{"prompt_tokens":1000,"completion_tokens":10,"cached_tokens":600,"prompt_tokens_details":{"cached_tokens":600}}`
	var u Usage
	if err := json.Unmarshal([]byte(body), &u); err != nil {
		t.Fatal(err)
	}
	if u.CachedTokens != 600 {
		t.Fatalf("命中量 = %d，期望 600（不应累加）", u.CachedTokens)
	}
	if r := u.CachedRatio(); r < 0.59 || r > 0.61 {
		t.Fatalf("命中率 = %.2f，期望 0.6", r)
	}
}

// 前缀比较要能指出第一个不同点。
func TestStablePrefix(t *testing.T) {
	a := []Message{{Role: "system", Content: "S"}, {Role: "user", Content: "U1"}}
	b := []Message{{Role: "system", Content: "S"}, {Role: "user", Content: "U2"}}
	if n := StablePrefix(a, b); n != 1 {
		t.Fatalf("共同前缀 = %d，期望 1", n)
	}
	if n := StablePrefix(a, a); n != 2 {
		t.Fatalf("自身比较 = %d，期望 2", n)
	}
	if n := StablePrefix(a, []Message{{Role: "system", Content: "S"}}); n != 1 {
		t.Fatalf("短序列 = %d，期望 1", n)
	}
}

func TestStableTextPrefix(t *testing.T) {
	if n := StableTextPrefix("你好世界", "你好，世界"); n != len("你好") {
		t.Fatalf("公共前缀 = %d，期望 %d", n, len("你好"))
	}
	if n := StableTextPrefix("abc", "abcdef"); n != 3 {
		t.Fatalf("公共前缀 = %d，期望 3", n)
	}
	if n := StableTextPrefix("abc", "xyz"); n != 0 {
		t.Fatalf("公共前缀 = %d，期望 0", n)
	}
}

func TestHasVolatilePrefix(t *testing.T) {
	if _, ok := HasVolatilePrefix("你是助手，当前时间是 12:00"); !ok {
		t.Fatal("应检测出易变前缀")
	}
	if _, ok := HasVolatilePrefix("你是助手，遵守 AGENTS.md"); ok {
		t.Fatal("稳定提示不应误报")
	}
}

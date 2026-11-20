package core

import (
	"math"
	"testing"
)

// v1.32 不再按模型名查价：任何模型都只能拿到默认价。
func TestLookupPriceNoBuiltInTable(t *testing.T) {
	for _, m := range []string{"gpt-4o", "gpt-4o-mini", "deepseek-chat", "claude-3-5-sonnet", "some-unknown-model"} {
		if p, precise := LookupPrice(m); precise || p != DefaultPrice {
			t.Fatalf("LookupPrice(%q) = %+v precise=%v，期望默认价且非精确", m, p, precise)
		}
	}
	if p, precise := LookupPrice(""); precise || p != DefaultPrice {
		t.Fatal("空模型名应回落到默认价")
	}
}

func TestCostWithCache(t *testing.T) {
	// 1M 输入里 800k 命中缓存，200k 未命中 + 100k 输出。
	got := Cost("any-model", 1000000, 800000, 100000)
	want := 0.2*DefaultPrice.In + 0.8*DefaultPrice.Cached + 0.1*DefaultPrice.Out
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("费用 = %.6f，期望 %.6f", got, want)
	}
	withCache := Cost("any-model", 1000000, 900000, 1000)
	noCache := CostNoCache("any-model", 1000000, 1000)
	if withCache >= noCache {
		t.Fatalf("缓存后费用 %.4f 应小于未缓存 %.4f", withCache, noCache)
	}
	if v := Cost("any-model", 100, 500, 0); v < 0 {
		t.Fatalf("封顶后不应为负：%v", v)
	}
}

func TestMoneyFormat(t *testing.T) {
	cases := map[float64]string{0: "$0", 0.00123: "$0.0012", 0.0356: "$0.036", 12.3456: "$12.35"}
	for in, want := range cases {
		if got := Money(in); got != want {
			t.Fatalf("Money(%v) = %s，期望 %s", in, got, want)
		}
	}
}

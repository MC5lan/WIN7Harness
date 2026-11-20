package agent

import "testing"

func TestCalcBudgetNoLimit(t *testing.T) {
	bs := calcBudget(1000, 500, 2, 1.0, 2.0, 0, 0, true)
	if bs.Exceeded || bs.Warn {
		t.Fatalf("未设限额时不应告警: %+v", bs)
	}
	if bs.CostUSD <= 0 {
		t.Fatalf("应按单价算出费用，实际 %v", bs.CostUSD)
	}
	if bs.TokIn+bs.TokOut != 1500 || bs.Turns != 2 {
		t.Fatalf("用量统计错误: %+v", bs)
	}
}

func TestCalcBudgetCostExceeded(t *testing.T) {
	// 单价 10 美元/百万 token，用 1M 输入 = 10 美元，上限 5
	bs := calcBudget(1_000_000, 0, 3, 10, 0, 5, 0, true)
	if !bs.Exceeded {
		t.Fatalf("应判定超限: %+v", bs)
	}
	if bs.Reason == "" {
		t.Fatal("超限时应给出原因")
	}
}

func TestCalcBudgetTokenWarnThenExceed(t *testing.T) {
	warn := calcBudget(850, 0, 1, 0, 0, 0, 1000, true)
	if !warn.Warn || warn.Exceeded {
		t.Fatalf("85%% 应为提醒不超限: %+v", warn)
	}
	over := calcBudget(1000, 0, 1, 0, 0, 0, 1000, true)
	if !over.Exceeded {
		t.Fatalf("100%% 应超限: %+v", over)
	}
}

func TestCalcBudgetWarnDisabled(t *testing.T) {
	bs := calcBudget(1000, 0, 1, 0, 0, 0, 1000, false)
	if bs.Warn {
		t.Fatal("关闭提醒后不应产生 warn")
	}
	if !bs.Exceeded {
		t.Fatal("超限判定不应受提醒开关影响")
	}
}

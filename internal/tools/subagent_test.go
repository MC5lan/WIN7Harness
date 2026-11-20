package tools

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseSubTasksSingle(t *testing.T) {
	list, err := parseSubTasks(&Context{}, map[string]interface{}{
		"prompt":      "  查一下配置  ",
		"description": "查配置",
	}, 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(list) != 1 || list[0].prompt != "查一下配置" || list[0].desc != "查配置" || !list[0].readOnly {
		t.Fatalf("bad parse: %+v", list)
	}
}

func TestParseSubTasksArrayAndLimit(t *testing.T) {
	list, err := parseSubTasks(&Context{}, map[string]interface{}{
		"tasks": []interface{}{
			map[string]interface{}{"prompt": "a", "read_only": false},
			map[string]interface{}{"prompt": "b"},
		},
	}, 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2, got %d", len(list))
	}
	if list[0].readOnly || !list[1].readOnly {
		t.Fatalf("read_only 解析错误: %+v", list)
	}
	if list[0].desc != "子任务1" {
		t.Fatalf("缺省描述未补齐: %q", list[0].desc)
	}

	_, err = parseSubTasks(&Context{}, map[string]interface{}{
		"tasks": []interface{}{
			map[string]interface{}{"prompt": "a"},
			map[string]interface{}{"prompt": "b"},
			map[string]interface{}{"prompt": "c"},
			map[string]interface{}{"prompt": "d"},
		},
	}, 0)
	if err == nil || !strings.Contains(err.Error(), "最多派发") {
		t.Fatalf("超限应报错，得到 %v", err)
	}

	if _, err := parseSubTasks(&Context{}, map[string]interface{}{}, 0); err == nil {
		t.Fatal("空参数应报错")
	}
}

// 上限可配置：调到 5 后 5 个任务应放行，6 个仍拒绝；超过硬上限自动回落。
func TestParseSubTasksConfigurableLimit(t *testing.T) {
	mk := func(n int) map[string]interface{} {
		items := make([]interface{}, n)
		for i := range items {
			items[i] = map[string]interface{}{"prompt": fmt.Sprintf("t%d", i)}
		}
		return map[string]interface{}{"tasks": items}
	}
	if _, err := parseSubTasks(&Context{}, mk(5), 5); err != nil {
		t.Fatalf("上限 5 时 5 个任务应放行: %v", err)
	}
	if _, err := parseSubTasks(&Context{}, mk(6), 5); err == nil {
		t.Fatal("上限 5 时 6 个任务应拒绝")
	}
	if _, err := parseSubTasks(&Context{}, mk(8), 99); err != nil {
		t.Fatalf("超硬上限应回落为 8 并放行 8 个: %v", err)
	}
	if _, err := parseSubTasks(&Context{}, mk(9), 99); err == nil {
		t.Fatal("硬上限 8 时 9 个任务应拒绝")
	}
	if got := subTaskLimit(0); got != defaultMaxSubTasks {
		t.Fatalf("0 应回落默认值, got %d", got)
	}
}

func TestRunTaskReadOnlyParallel(t *testing.T) {
	var inflight, peak int32
	ctx := &Context{
		Spawn: func(prompt, desc string, readOnly bool) (string, error) {
			n := atomic.AddInt32(&inflight, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
					break
				}
			}
			time.Sleep(120 * time.Millisecond)
			atomic.AddInt32(&inflight, -1)
			return "结论-" + prompt, nil
		},
	}
	res, err := runTask(ctx, map[string]interface{}{
		"tasks": []interface{}{
			map[string]interface{}{"prompt": "A", "description": "甲"},
			map[string]interface{}{"prompt": "B", "description": "乙"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if atomic.LoadInt32(&peak) < 2 {
		t.Fatalf("只读任务应并行执行，峰值并发=%d", peak)
	}
	if !strings.Contains(res.Output, "结论-A") || !strings.Contains(res.Output, "结论-B") {
		t.Fatalf("结果缺失: %s", res.Output)
	}
	if strings.Index(res.Output, "甲") > strings.Index(res.Output, "乙") {
		t.Fatalf("结果顺序应与派发顺序一致: %s", res.Output)
	}
}

func TestRunTaskWriteFallsBackToSerial(t *testing.T) {
	var inflight, peak int32
	ctx := &Context{
		Spawn: func(prompt, desc string, readOnly bool) (string, error) {
			n := atomic.AddInt32(&inflight, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
					break
				}
			}
			time.Sleep(60 * time.Millisecond)
			atomic.AddInt32(&inflight, -1)
			return "ok-" + prompt, nil
		},
	}
	_, err := runTask(ctx, map[string]interface{}{
		"tasks": []interface{}{
			map[string]interface{}{"prompt": "A"},
			map[string]interface{}{"prompt": "B", "read_only": false},
		},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if atomic.LoadInt32(&peak) != 1 {
		t.Fatalf("含写任务时应串行，峰值并发=%d", peak)
	}
}

func TestRunTaskPartialFailure(t *testing.T) {
	ctx := &Context{
		Spawn: func(prompt, desc string, readOnly bool) (string, error) {
			if prompt == "bad" {
				return "", fmt.Errorf("炸了")
			}
			return "fine", nil
		},
	}
	res, err := runTask(ctx, map[string]interface{}{
		"tasks": []interface{}{
			map[string]interface{}{"prompt": "bad", "description": "坏"},
			map[string]interface{}{"prompt": "good", "description": "好"},
		},
	})
	if err != nil {
		t.Fatalf("部分失败不应整体报错: %v", err)
	}
	if !strings.Contains(res.Output, "失败") || !strings.Contains(res.Output, "fine") {
		t.Fatalf("输出应同时含失败与成功: %s", res.Output)
	}
}

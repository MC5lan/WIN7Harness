package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecBatchParallelOrder(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("AAA"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("BBB"), 0o644)

	e := New(nil, "test", dir)
	calls := []pendingCall{
		{id: "1", name: "read_file", args: `{"path":"a.txt"}`},
		{id: "2", name: "read_file", args: `{"path":"b.txt"}`},
		{id: "3", name: "list_dir", args: `{}`},
		{id: "4", name: "nope", args: `{}`},
	}
	outs, _, durs := e.execBatch(context.Background(), calls)
	if !strings.Contains(outs[0], "AAA") || !strings.Contains(outs[1], "BBB") {
		t.Fatalf("并行读取结果错位: %q / %q", outs[0], outs[1])
	}
	if !strings.Contains(outs[2], "a.txt") {
		t.Fatalf("list_dir 异常: %q", outs[2])
	}
	if len(durs) != len(calls) {
		t.Fatalf("耗时数组长度不对: %d", len(durs))
	}
	if !strings.HasPrefix(outs[3], "错误：不存在工具") {
		t.Fatalf("未知工具应报错: %q", outs[3])
	}
}

func TestCompactNoop(t *testing.T) {
	e := New(nil, "test", t.TempDir())
	msg, err := e.Compact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "无需压缩") {
		t.Fatalf("短会话不应压缩: %q", msg)
	}
}

func TestPlanModeBlocksWriteTools(t *testing.T) {
	e := New(nil, "test-model", ".")
	e.PlanMode = true

	out, _ := e.execTool("write_file", `{"path":"plan_mode_probe.txt","content":"x"}`)
	if !strings.HasPrefix(out, "错误：") || !strings.Contains(out, "计划模式") {
		t.Fatalf("计划模式未拦截写盘: %q", out)
	}
	if _, err := os.Stat("plan_mode_probe.txt"); err == nil {
		os.Remove("plan_mode_probe.txt")
		t.Fatal("计划模式不应真正写入文件")
	}

	// 只读工具照常可用
	out, _ = e.execTool("list_dir", `{"path":"."}`)
	if strings.HasPrefix(out, "错误：") {
		t.Fatalf("只读工具被误拦截: %q", out)
	}

	// 退出计划模式后恢复
	e.PlanMode = false
	if !strings.Contains(planModeBlock(), "计划模式") {
		t.Fatal("计划模式提示词缺失")
	}
}

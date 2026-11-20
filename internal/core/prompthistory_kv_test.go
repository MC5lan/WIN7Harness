package core

import (
	"fmt"
	"sync"
	"testing"
)

func TestPromptHistoryPathIsKV(t *testing.T) {
	if got := PromptHistoryPath(); got != KVPath() {
		t.Fatalf("历史应落在 KV: got %s want %s", got, KVPath())
	}
}

// TestPromptHistoryKVRoundTrip 验证 JSON→KV 后端迁移后的读写语义。
// 唯一初始化 DataKV 单例的测试：用 WIN7HARNESS_HOME 隔离；结束后关闭并
// 重置单例，避免 Windows 上 db 句柄占住临时目录导致清理失败。
func TestPromptHistoryKVRoundTrip(t *testing.T) {
	t.Setenv("WIN7HARNESS_HOME", t.TempDir())
	defer func() {
		if dataKVInst != nil {
			_ = dataKVInst.Close()
			dataKVInst = nil
			dataKVErr = nil
			dataKVOnce = sync.Once{}
		}
	}()

	if got := LoadPromptHistory(); len(got) != 0 {
		t.Fatalf("空历史应返回空表: %v", got)
	}
	items := LoadPromptHistory()
	items = AddPromptHistory(items, "第一条")
	items = AddPromptHistory(items, "第二条")
	items = AddPromptHistory(items, "第一条") // 去重后移到末尾
	if err := SavePromptHistory(items); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := LoadPromptHistory()
	if len(got) != 2 || got[0] != "第二条" || got[1] != "第一条" {
		t.Fatalf("KV 往返内容/顺序不符: %v", got)
	}

	big := make([]string, promptHistoryMax+50)
	for i := range big {
		big[i] = fmt.Sprintf("输入%d", i)
	}
	if err := SavePromptHistory(big); err != nil {
		t.Fatal(err)
	}
	if got := LoadPromptHistory(); len(got) != promptHistoryMax {
		t.Fatalf("上限裁剪不符: %d", len(got))
	}
}

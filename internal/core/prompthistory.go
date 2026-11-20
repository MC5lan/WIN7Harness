package core

import (
	"strings"
)

// 用户输入的持久化历史（Ctrl+R 搜索的数据源）。
//
// 存储后端：harness.kv.db（与任务队列共用一个 KV 文件，不新增文件）。
// 对外 API 签名保持不变（PromptHistoryPath / Load / Save / Add），
// UI 侧 app.go 的 send() 无需改动。
//
// 顺序约定：旧 → 新，最新一条在末尾（与 AddPromptHistory 的 append 语义一致）。
// Save 走 KV 的 Update 事务（读-裁剪-写一次提交），断电不会留下半截历史。

const (
	promptHistoryBucket = "prompthistory"
	promptHistoryKey    = "recent"
	promptHistoryMax    = 200
)

// PromptHistoryPath 保留兼容：返回历史实际所在的 KV 数据库路径。
// 历史已从独立的 prompt-history.json 迁到 harness.kv.db；
// 调用方不应依赖具体文件名（仅供诊断/展示）。
func PromptHistoryPath() string { return KVPath() }

// LoadPromptHistory 从 KV 读取历史。读不到或数据损坏时返回空表，
// 不报错——历史是增强功能，不该打断启动。
func LoadPromptHistory() []string {
	kv, err := DataKV()
	if err != nil {
		return nil
	}
	var items []string
	found, err := kv.Get(promptHistoryBucket, promptHistoryKey, &items)
	if err != nil || !found {
		return nil
	}
	return items
}

// SavePromptHistory 把历史写入 KV。超过上限只留最近 promptHistoryMax 条，
// 写盘由 bbolt 事务保证原子（替代旧的 WriteFileAtomic + JSON）。
func SavePromptHistory(items []string) error {
	if len(items) > promptHistoryMax {
		items = items[len(items)-promptHistoryMax:]
	}
	kv, err := DataKV()
	if err != nil {
		return err
	}
	return kv.Put(promptHistoryBucket, promptHistoryKey, items)
}

// AddPromptHistory 追加一条提示词：去掉首尾空白、丢弃空串与 / 开头的命令、
// 全表去重（保留最后一次输入的位置）、上限 promptHistoryMax 条。
// 纯函数，不碰存储——调用方拿到结果后自行 Save。
func AddPromptHistory(items []string, text string) []string {
	text = strings.TrimSpace(text)
	if text == "" || strings.HasPrefix(text, "/") {
		return items
	}
	if len(items) > 0 && items[len(items)-1] == text {
		return items
	}
	kept := items[:0]
	for _, s := range items {
		if s != text {
			kept = append(kept, s)
		}
	}
	kept = append(kept, text)
	if len(kept) > promptHistoryMax {
		kept = kept[len(kept)-promptHistoryMax:]
	}
	return kept
}

// RecordPrompt 记入跨会话提示词历史（Ctrl+R 用）：加载 → 去重追加 → 落盘。
// 写盘失败不阻断发送；与 send() 直接调 Add+Save 等价，作为更小的调用面给 recovery/入口用。
// （若上层已带内存快照，可直接调 Load/Add/Save 三步；此处不做快照，只保证“能工作”。）
func RecordPrompt(text string) {
	items := LoadPromptHistory()
	items = AddPromptHistory(items, text)
	_ = SavePromptHistory(items)
}

package core

// —— 三份落盘清单（设备表 / 守护规则 / 定时任务）共用的存储基座 ——
//
// 审计结论（重构方案第五节 #4）：watch / schedule / devices 三个 Store
// 各复制一份 Load / Quarantine / Save / 锁，缺陷修了三遍
// （CR-P1-5 加锁防 concurrent map access、CR-P2-10 原子化、CR-P0-x 崩溃隔离）。
// 这里合并成两处：读盘坏盘隔离（Quarantine + 用户可读的 LoadErr）、原子写（WriteFileAtomic）。
// 结构体形状与业务方法留在各自文件；权限统一 0600（内部含路径/提示词等敏感信息）。
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// loadStoreFile 读取并解析清单文件。
//
//	found=true                      → 解析成功，v 已被填充，调用方按正常路径继续
//	found=false 且 loadErr==""      → 文件不存在或为空，调用方按空清单处理（不报错）
//	found=false 且 loadErr!=""      → 文件损坏；已 Quarantine 改名备份（.bad-纳秒时间戳），
//	                                  返回给用户看的原因（原文件保留以便排查）
//
// badMsgFmt 是形如 "守护规则损坏，已备份为 %s" 的完整模板，%s 填备份文件名。
func loadStoreFile(path, badMsgFmt string, v interface{}) (found bool, loadErr string) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return false, ""
	}
	if err := json.Unmarshal(data, v); err == nil {
		return true, ""
	}
	if bak, qerr := Quarantine(path); qerr == nil {
		return false, fmt.Sprintf(badMsgFmt, filepath.Base(bak))
	}
	return false, fmt.Sprintf(badMsgFmt, "（.bad 备份失败：文件可能被占用）")
}

// writeStoreAtomic 原子写入：MkdirAll → MarshalIndent → WriteFileAtomic。
// 权限固定 0600（清单内容含工作区路径/提示词/设备哈希，不该被同机其它用户读）。
func writeStoreAtomic(path string, v interface{}) error {
	if path == "" {
		return fmt.Errorf("未指定清单路径")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, data, 0o600)
}

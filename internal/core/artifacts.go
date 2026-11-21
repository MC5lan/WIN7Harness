package core

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 工具输出全文仓库：超长命令/测试/grep 输出只把摘要回灌模型，
// 全文落在本地，供用户点“查看完整输出”或模型用 read_file 按需读取。
// 只存本机，不上传；定期清理，避免把磁盘吃满。

// ArtifactsDir 返回全文仓库根目录。
func ArtifactsDir() string { return filepath.Join(LocalDataDir(), "artifacts") }

// SpillArtifact 把一次工具输出写到 artifacts 下，返回可读路径。
// 文件名带工作区短哈希 + 工具名 + 时间，避免不同项目串在一起。
func SpillArtifact(workDir, tool, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	dir := ArtifactsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	sum := sha1.Sum([]byte(strings.ToLower(filepath.Clean(workDir))))
	base := "unknown"
	if strings.TrimSpace(tool) != "" {
		base = sanitizeArtifactName(tool)
	}
	name := fmt.Sprintf("%s-%s-%d.txt", hex.EncodeToString(sum[:4]), base, time.Now().UnixNano())
	p := filepath.Join(dir, name)
	if err := WriteFileAtomic(p, []byte(text), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

func sanitizeArtifactName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		out = "tool"
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

// PruneArtifacts 清理过期/过多的全文文件。maxAge<=0 用 30 天，maxFiles<=0 用 800。
// 只删除普通文件；失败静默，不影响主流程。
func PruneArtifacts(maxAge time.Duration, maxFiles int) {
	if maxAge <= 0 {
		maxAge = 30 * 24 * time.Hour
	}
	if maxFiles <= 0 {
		maxFiles = 800
	}
	dir := ArtifactsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		path string
		mod  time.Time
		size int64
	}
	items := make([]item, 0, len(entries))
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(st.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, e.Name()))
			continue
		}
		items = append(items, item{filepath.Join(dir, e.Name()), st.ModTime(), st.Size()})
	}
	if len(items) <= maxFiles {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.Before(items[j].mod) })
	for i := 0; i < len(items)-maxFiles; i++ {
		_ = os.Remove(items[i].path)
	}
}

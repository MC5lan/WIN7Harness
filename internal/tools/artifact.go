package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"win7harness/internal/core"
)

func init() {
	register(Tool{
		Name: "read_artifact",
		Desc: "读取工具全文仓库中的输出（超长 run_command / run_tests / grep 等结果会保存到 artifacts）。" +
			"path 用工具结果里给出的完整路径；支持 offset/limit 分段，避免一次读回全文。",
		Schema: `{
  "type":"object",
  "properties":{
    "path":{"type":"string","description":"artifact 完整路径（来自工具结果的“完整输出已保存到 …”）"},
    "offset":{"type":"integer","description":"起始行号（从 1 开始），默认 1"},
    "limit":{"type":"integer","description":"最多读取行数，默认 400"}
  },
  "required":["path"]
}`,
		Run: runReadArtifact,
	})
}

func runReadArtifact(ctx *Context, args map[string]interface{}) (Result, error) {
	raw := strings.TrimSpace(Get(args, "path"))
	if raw == "" {
		return Result{}, fmt.Errorf("path 为空")
	}
	p := core.ResolvePath(ctx.WorkDir, raw)
	root := core.ArtifactsDir()
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return Result{}, fmt.Errorf("只允许读取工具全文仓库内的文件：%s", root)
	}
	st, err := os.Stat(p)
	if err != nil {
		return Result{}, fmt.Errorf("artifact 不存在：%s", p)
	}
	if st.IsDir() {
		return Result{}, fmt.Errorf("%s 是目录", p)
	}
	if st.Size() > 16<<20 {
		return Result{}, fmt.Errorf("artifact 过大（%s），请用别的工具分段处理", core.HumanSize(st.Size()))
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Result{}, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	offset := GetInt(args, "offset", 1)
	if offset < 1 {
		offset = 1
	}
	limit := GetInt(args, "limit", 400)
	if limit <= 0 {
		limit = 400
	}
	if offset > len(lines) {
		offset = len(lines)
	}
	end := offset - 1 + limit
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "artifact: %s\n行数: %d，显示第 %d-%d 行\n\n", p, len(lines), offset, end)
	for i := offset - 1; i < end; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, lines[i])
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "\n…（还有 %d 行，用 offset/limit 继续读取）", len(lines)-end)
	}
	return Result{Output: b.String(), Display: true, Title: "工具全文"}, nil
}

package evolve

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// MaxProposals 一轮最多接收的提案数。真正执行时仍然一次只做一个。
const MaxProposals = 3

// ParseProposals 从模型输出里抠出提案数组，容忍代码块和前后说明。
func ParseProposals(raw string) ([]Proposal, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("提案输出为空")
	}
	if i := strings.Index(s, "["); i >= 0 {
		if j := strings.LastIndex(s, "]"); j > i {
			s = s[i : j+1]
		}
	}
	var arr []Proposal
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil, fmt.Errorf("无法解析提案 JSON：%w", err)
	}
	out := make([]Proposal, 0, len(arr))
	for _, p := range arr {
		p = NormalizeProposal(p)
		if err := ValidateProposal(p); err != nil {
			// 单条不合法不拖垮整批：模型偶尔会混进一条空壳提案。
			continue
		}
		out = append(out, p)
		if len(out) >= MaxProposals {
			break
		}
	}
	if len(out) == 0 {
		if len(arr) == 0 {
			return []Proposal{}, nil
		}
		return nil, fmt.Errorf("没有可用提案（共解析到 %d 条）", len(arr))
	}
	return out, nil
}

// NormalizeProposal 做保守归一化：风险缺省按 C 处理，宁可少合并。
func NormalizeProposal(p Proposal) Proposal {
	p.Title = strings.TrimSpace(p.Title)
	p.Hypothesis = strings.TrimSpace(p.Hypothesis)
	p.Change = strings.TrimSpace(p.Change)
	p.Acceptance = strings.TrimSpace(p.Acceptance)
	p.ExpectedMetric = strings.TrimSpace(p.ExpectedMetric)
	p.Risk = strings.ToUpper(strings.TrimSpace(p.Risk))
	switch p.Risk {
	case RiskA, RiskB, RiskC:
	default:
		p.Risk = RiskC
	}
	var files []string
	for _, f := range p.Files {
		f = strings.TrimSpace(strings.ReplaceAll(f, "\\", "/"))
		if f != "" {
			files = append(files, f)
		}
	}
	p.Files = files
	return p
}

// ValidateProposal 只做结构性校验；路径安全由门禁的受保护路径扫描兜底。
func ValidateProposal(p Proposal) error {
	if p.Title == "" {
		return fmt.Errorf("缺少 title")
	}
	if p.Change == "" {
		return fmt.Errorf("缺少 change")
	}
	if p.Acceptance == "" {
		return fmt.Errorf("缺少 acceptance")
	}
	for _, f := range p.Files {
		if filepath.IsAbs(f) || strings.Contains(f, "..") {
			return fmt.Errorf("文件路径不合法：%s", f)
		}
	}
	return nil
}

// BuildProposePrompt 构造提案器提示词。
func BuildProposePrompt(in ProposeInput) string {
	var b strings.Builder
	b.WriteString("你是 WIN7 Harness 的自迭代提案器。项目是 Go 1.20 编写的 Windows 7 兼容 agent harness。\n")
	b.WriteString("你的任务不是写代码，而是从信号里挑出 1-3 个最小、可验证、低风险的改进提案。\n\n")
	b.WriteString("硬约束：\n")
	b.WriteString("- 一次只改一件事；单条提案的 diff 控制在 200 行以内。\n")
	b.WriteString("- 不要提议修改 internal/eval、internal/selftest、internal/evolve、cmd/evolve、build.sh、go.mod/go.sum。\n")
	b.WriteString("- 不要提议删除测试、跳过测试、改门禁、改权限边界。\n")
	b.WriteString("- 每条提案必须有可自动验证的 acceptance（比如某个测试从失败变通过、某个命令输出变化）。\n")
	b.WriteString("- 风险分级：A=测试/文档/无行为重构；B=修 bug/性能/错误信息；C=权限/存储/更新器/Win32/依赖/构建发布。\n")
	b.WriteString("- 如果信号不足，宁可返回空数组 []。\n\n")
	b.WriteString("只输出 JSON 数组，不要 Markdown 代码块，不要解释。\n")
	b.WriteString("JSON 必须严格合法：字符串用双引号，字符串内部不要出现原始换行，不要用中文引号/中文逗号做结构符号。字段：\n")
	b.WriteString(`[{"title":"...","hypothesis":"...","files":["..."],"change":"...","acceptance":"...","risk":"A|B|C","expected_metric":"..."}]` + "\n\n")

	if strings.TrimSpace(in.Goal) != "" {
		b.WriteString("用户指定目标：\n" + strings.TrimSpace(in.Goal) + "\n\n")
	}
	if len(in.Signals) > 0 {
		b.WriteString("当前信号：\n")
		for _, s := range in.Signals {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			b.WriteString("- " + clip(s, 300) + "\n")
		}
		b.WriteString("\n")
	}
	if len(in.History) > 0 {
		b.WriteString("最近的实验（避免重复失败）：\n")
		start := 0
		if len(in.History) > 8 {
			start = len(in.History) - 8
		}
		for _, e := range in.History[start:] {
			b.WriteString(fmt.Sprintf("- [%s] %s（风险 %s）：%s\n",
				e.Status, clip(e.Title, 100), e.Risk, clip(e.Reason, 200)))
		}
		b.WriteString("\n")
	}
	b.WriteString("请给出提案。")
	return b.String()
}

// BuildImplementPrompt 构造实现器提示词。
func BuildImplementPrompt(in ImplementInput) string {
	var b strings.Builder
	b.WriteString("你在一个隔离的 git worktree 中实现一个自迭代提案。\n")
	b.WriteString("项目：WIN7 Harness（Go 1.20，Windows 7 兼容，需同时通过 386/amd64 构建）。\n")
	b.WriteString("工作目录： " + in.WorkDir + "\n\n")
	b.WriteString("提案：\n")
	b.WriteString(fmt.Sprintf("- 标题：%s\n- 假设：%s\n- 改动：%s\n- 验收：%s\n- 风险：%s\n",
		in.Proposal.Title, in.Proposal.Hypothesis, in.Proposal.Change,
		in.Proposal.Acceptance, in.Proposal.Risk))
	if len(in.Proposal.Files) > 0 {
		b.WriteString("- 预计涉及文件：" + strings.Join(in.Proposal.Files, ", ") + "\n")
	}
	b.WriteString("\n要求：\n")
	b.WriteString("- 先读代码再改；只做提案范围内最小的改动，不要顺手重构。\n")
	b.WriteString("- 不要修改 internal/eval、internal/selftest、internal/evolve、cmd/evolve、build.sh、go.mod/go.sum。\n")
	b.WriteString("- 不要执行 git 写操作（commit/checkout/reset/push），提交由外层负责。\n")
	b.WriteString("- 不要访问网络，不要删除测试，不要用 TODO 占位。\n")
	b.WriteString("- 改完至少运行 `go build ./...`；只跑目标包的定向测试（如 `go test ./internal/core -run TestX`）。\n")
	b.WriteString("- 不要运行全量 `go test ./...`，也不要用 run_tests 工具：全量测试由外层门禁统一执行，重复跑会拖慢整轮。\n")
	b.WriteString("- 也不要运行整个 `internal/core` 包的测试（不带 `-run` 会跑十几分钟并触发默认 10 分钟超时）；只跑 `-run TestXxx` 的定向用例。\n")
	b.WriteString("- 用 `edit_file` / `write_file` 改代码，不要用 `apply_patch`（无人值守下会被拒绝）。\n")
	b.WriteString("- 如果 edit_file 失败，读回文件确认上下文后重试；不要用 AGENTS.md 等自动生成文件冒充改动。\n")
	b.WriteString("- 收尾前用 `git diff --stat`（只读）确认改动真实存在；不要翻 git 历史，也不要把多条命令用 `&&` 串起来。\n")
	b.WriteString("- 如果没有任何源码改动，明确说明失败原因，不要假装完成。\n\n")
	b.WriteString("最后用一段话总结：改了哪些文件、跑了什么验证、还有什么没覆盖。")
	return b.String()
}

// clip 按字符截断，避免多字节字符被切坏。
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

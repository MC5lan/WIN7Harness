package core

import (
	"fmt"
	"path/filepath"
	"strings"
)

// 权限与审批策略：把"要不要问用户"从散落在各个工具里的临时判断，
// 收拢成一份可读、可改、可解释的规则表。
//
// 设计取舍：
//   - 规则只有三类（allow / deny / ask），按 deny → allow → ask → 模式默认 的顺序判定，
//     保证"明确拒绝"永远压过"自动放行"，避免一条宽泛的 allow 把危险操作放出来；
//   - 规则写成 "工具:模式串"，工具可用 * 通配；模式串支持 * 与 ?，不含通配符时按前缀匹配
//     （命令行习惯），这样 `git push` 能同时覆盖 `git push origin main`；
//   - 目录白名单只对写类工具生效：工作区之外还有需要长期写的目录（如 G:\build），
//     加进来就不会每次弹确认。

// 审批模式。
const (
	PermAsk  = "ask"  // 默认：写类与危险操作询问
	PermAuto = "auto" // 非高危自动放行
	PermPlan = "plan" // 只读：所有写类操作一律拒绝
)

// PermDecision 是一次权限判定的结果。
type PermDecision struct {
	// Kind 取值 allow / deny / ask。
	Kind string
	// Reason 给用户看的判定依据，会出现在确认框标题或拒绝原因里。
	Reason string
}

func (d PermDecision) Allow() bool { return d.Kind == "allow" }
func (d PermDecision) Deny() bool  { return d.Kind == "deny" }

// Policy 是一份可热更新的审批策略。
type Policy struct {
	// Mode 为 ask / auto / plan。
	Mode string
	// Allow / Deny / Ask 为规则列表，元素形如 "run_command:git *"。
	Allow []string
	Deny  []string
	Ask   []string
	// Dirs 为额外允许直接写入的目录（工作区之外）。
	Dirs []string
}

// NewPolicy 构造策略，非法模式回落到 ask（安全默认）。
func NewPolicy(mode string, allow, deny, ask, dirs []string) *Policy {
	p := &Policy{Mode: normalizePermMode(mode)}
	p.Allow = cleanRules(allow)
	p.Deny = cleanRules(deny)
	p.Ask = cleanRules(ask)
	for _, d := range dirs {
		if d = strings.TrimSpace(d); d != "" {
			p.Dirs = append(p.Dirs, d)
		}
	}
	return p
}

func normalizePermMode(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case PermAuto:
		return PermAuto
	case PermPlan:
		return PermPlan
	default:
		return PermAsk
	}
}

func cleanRules(in []string) []string {
	var out []string
	for _, r := range in {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// Clone 复制一份，供子 Agent 使用，避免并发改同一份规则。
func (p *Policy) Clone() *Policy {
	if p == nil {
		return nil
	}
	q := &Policy{Mode: p.Mode}
	q.Allow = append([]string{}, p.Allow...)
	q.Deny = append([]string{}, p.Deny...)
	q.Ask = append([]string{}, p.Ask...)
	q.Dirs = append([]string{}, p.Dirs...)
	return q
}

// Decide 判定一次工具调用。
//   - tool：工具名
//   - target：命令文本或文件绝对路径（用于规则匹配）
//   - write：是否写类/执行类工具
//   - danger：命令风险等级（仅 run_command 有意义，其它传 RiskSafe）
//   - workDir：当前工作区，用于判断是否越界
func (p *Policy) Decide(tool, target string, write bool, danger RiskLevel, workDir string) PermDecision {
	if p == nil {
		return PermDecision{Kind: "allow", Reason: "未启用策略"}
	}
	tool = strings.TrimSpace(tool)
	target = strings.TrimSpace(target)

	if rule, ok := matchRules(p.Deny, tool, target); ok {
		return PermDecision{Kind: "deny", Reason: "命中拒绝规则 " + rule}
	}
	// 高危命令无论什么模式都要确认：auto 模式是为了少打扰，不是为了让 rm -rf 静默执行。
	// 注意必须在 allow 判定之前——allow 不能成为绕过高危确认的路径。
	if danger == RiskDanger {
		if p.Mode == PermPlan {
			return PermDecision{Kind: "deny", Reason: "计划模式（只读）下禁止执行高危命令"}
		}
		return PermDecision{Kind: "ask", Reason: "高危命令需逐次确认"}
	}
	allowRule, allowed := matchRules(p.Allow, tool, target)
	if allowed {
		return PermDecision{Kind: "allow", Reason: "命中放行规则 " + allowRule}
	}
	// auto 模式只自动放行 Safe。Caution（解释器、del/copy/move、计划任务等）
	// 仍必须确认：命令文本是用户/模型控制的，不能因为“看起来不致命”就静默执行。
	if danger == RiskCaution {
		if p.Mode == PermPlan {
			return PermDecision{Kind: "deny", Reason: "计划模式（只读）下禁止执行中危命令"}
		}
		return PermDecision{Kind: "ask", Reason: "中危命令需确认（auto 模式不自动放行解释器/删除/复制类命令）"}
	}
	if rule, ok := matchRules(p.Ask, tool, target); ok {
		return PermDecision{Kind: "ask", Reason: "命中询问规则 " + rule}
	}
	if write {
		if p.Mode == PermPlan {
			return PermDecision{Kind: "deny", Reason: "计划模式（只读）下禁止写操作"}
		}
		if escapesUnlisted(workDir, target, p.Dirs) {
			return PermDecision{Kind: "ask", Reason: "目标在工作区之外且未列入允许目录"}
		}
	}
	if p.Mode == PermAuto {
		return PermDecision{Kind: "allow", Reason: "auto 模式自动放行"}
	}
	if !write {
		return PermDecision{Kind: "allow", Reason: "只读操作"}
	}
	return PermDecision{Kind: "ask", Reason: "写类操作需确认"}
}

// escapesUnlisted 判断写目标是否越出工作区且不在白名单里。
// target 不是路径（命令文本等）时一律返回 false，交给命令风险分级处理。
func escapesUnlisted(workDir, target string, dirs []string) bool {
	if strings.TrimSpace(workDir) == "" || target == "" {
		return false
	}
	if !filepath.IsAbs(target) && !strings.ContainsAny(target, `/\`) {
		return false
	}
	if !EscapesWorkDir(workDir, target) {
		return false
	}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if !EscapesWorkDir(d, target) {
			return false
		}
	}
	return true
}

// matchRules 返回命中的第一条规则原文。
func matchRules(rules []string, tool, target string) (string, bool) {
	for _, r := range rules {
		if ruleMatches(r, tool, target) {
			return r, true
		}
	}
	return "", false
}

// ruleMatches：规则 "工具:模式"，工具缺省为 *。
func ruleMatches(rule, tool, target string) bool {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return false
	}
	toolPat := "*"
	pat := rule
	if i := strings.Index(rule, ":"); i >= 0 {
		toolPat = strings.TrimSpace(rule[:i])
		pat = strings.TrimSpace(rule[i+1:])
	}
	if toolPat != "" && toolPat != "*" && !wildMatch(strings.ToLower(toolPat), strings.ToLower(tool)) {
		return false
	}
	if pat == "" || pat == "*" {
		return true
	}
	if strings.ContainsAny(pat, "*?") {
		return wildMatch(pat, target)
	}
	// 无通配符：前缀匹配。必须带边界——“git push” 不该放行 “git pushxxx”，
	// "C:\\proj" 也不该命中 "C:\\projects"。命中的边界取空白/路径符/shell 分隔符之一。
	if !strings.HasPrefix(target, pat) {
		return false
	}
	if len(target) == len(pat) {
		return true
	}
	c := target[len(pat)]
	switch c {
	case ' ', '\t', '/', '\\', ';', '&', '|', '"', '\'', ')', '}', ']', '=', ',':
		return true
	}
	return false
}

// wildMatch 简化通配：* 匹配任意长度，? 匹配单个字符（按 rune，
// 中文按字符而不是按字节），其余按字面比较。
func wildMatch(pat, s string) bool {
	pr, sr := []rune(pat), []rune(s)
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(sr) {
		switch {
		case pi < len(pr) && (pr[pi] == '?' || pr[pi] == sr[si]):
			pi++
			si++
		case pi < len(pr) && pr[pi] == '*':
			star = pi
			mark = si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(pr) && pr[pi] == '*' {
		pi++
	}
	return pi == len(pr)
}

// SetMode 设置模式并返回是否合法。
func (p *Policy) SetMode(m string) bool {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case PermAsk, PermAuto, PermPlan:
		p.Mode = normalizePermMode(m)
		return true
	}
	return false
}

// Add 追加一条规则；kind 为 allow / deny / ask。返回规范化后的规则文本。
func (p *Policy) Add(kind, rule string) (string, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return "", fmt.Errorf("规则为空")
	}
	if !strings.Contains(rule, ":") {
		rule = "*:" + rule
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "allow":
		p.Allow = append(p.Allow, rule)
	case "deny":
		p.Deny = append(p.Deny, rule)
	case "ask":
		p.Ask = append(p.Ask, rule)
	default:
		return "", fmt.Errorf("未知规则类型 %q（可用 allow / deny / ask）", kind)
	}
	return rule, nil
}

// Remove 删除第 i 条规则（按 Summary 里的编号，从 1 开始）。
func (p *Policy) Remove(i int) (string, bool) {
	all := p.flat()
	if i < 1 || i > len(all) {
		return "", false
	}
	target := all[i-1]
	p.Allow = removeStr(p.Allow, target)
	p.Deny = removeStr(p.Deny, target)
	p.Ask = removeStr(p.Ask, target)
	return target, true
}

func removeStr(in []string, s string) []string {
	out := in[:0]
	removed := false
	for _, v := range in {
		if !removed && v == s {
			removed = true
			continue
		}
		out = append(out, v)
	}
	return out
}

// flat 按 deny / allow / ask 的顺序展开，编号与 Summary 一致。
func (p *Policy) flat() []string {
	var out []string
	out = append(out, p.Deny...)
	out = append(out, p.Allow...)
	out = append(out, p.Ask...)
	return out
}

// Summary 给界面看的策略说明。
func (p *Policy) Summary() string {
	var b strings.Builder
	b.WriteString("审批模式：" + p.Mode)
	switch p.Mode {
	case PermAuto:
		b.WriteString("（非高危自动放行，高危仍逐次确认）")
	case PermPlan:
		b.WriteString("（只读，一切写操作拒绝）")
	default:
		b.WriteString("（写类操作逐次确认）")
	}
	b.WriteString("\n\n规则（deny 优先于 allow）：")
	all := p.flat()
	if len(all) == 0 {
		b.WriteString("\n  （无）")
	}
	n := 0
	writeGroup := func(title string, rules []string) {
		if len(rules) == 0 {
			return
		}
		b.WriteString("\n  " + title)
		for _, r := range rules {
			n++
			b.WriteString(fmt.Sprintf("\n    %d. %s", n, r))
		}
	}
	writeGroup("拒绝：", p.Deny)
	writeGroup("放行：", p.Allow)
	writeGroup("询问：", p.Ask)
	if len(p.Dirs) > 0 {
		b.WriteString("\n\n额外可写目录：")
		for _, d := range p.Dirs {
			b.WriteString("\n  " + d)
		}
	}
	b.WriteString("\n\n用法：/perm mode ask|auto|plan · /perm allow|deny|ask <规则> · /perm del <编号> · /perm dir <目录>")
	return b.String()
}

// SplitPermRules 把配置里 "allow:run_command:go *" 形式的扁平规则拆成三类。
// 配置只存一份字符串列表，界面与命令改起来简单；语义在这里统一解释。
func SplitPermRules(rules []string) (allow, deny, ask []string) {
	for _, raw := range rules {
		r := strings.TrimSpace(raw)
		if r == "" {
			continue
		}
		kind, body, ok := strings.Cut(r, ":")
		if !ok {
			// 没写类型的一律当 allow，减少配置负担（默认放行示例类规则）。
			allow = append(allow, r)
			continue
		}
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "deny", "block", "forbid":
			deny = append(deny, body)
		case "ask", "confirm":
			ask = append(ask, body)
		case "allow", "permit", "ok":
			allow = append(allow, body)
		default:
			allow = append(allow, r)
		}
	}
	return
}

// NewPolicyFromRules 从配置构造策略；非法规则自动忽略而不报错。
func NewPolicyFromRules(mode string, rules, dirs []string) *Policy {
	allow, deny, ask := SplitPermRules(rules)
	return NewPolicy(mode, allow, deny, ask, dirs)
}

// FlatRules 把当前规则导出为配置格式（供命令写回配置文件）。
func (p *Policy) FlatRules() []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, r := range p.Deny {
		out = append(out, "deny:"+r)
	}
	for _, r := range p.Allow {
		out = append(out, "allow:"+r)
	}
	for _, r := range p.Ask {
		out = append(out, "ask:"+r)
	}
	return out
}

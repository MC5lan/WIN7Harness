package agent

import (
	"strings"
)

// looksLikePlan 判断一段回复是不是"改动方案"而不是普通回答。
//
// 计划模式下模型给出的方案，界面要能接住并渲染成"待确认"卡片，
// 用户点一下就按它执行。这里只做保守判定：宁可漏判（退化成普通回答，
// 用户仍可手动 /plan 退出），也不要错判（把普通答复当计划弹卡片）。
func looksLikePlan(text string) bool {
	t := strings.TrimSpace(text)
	if len([]rune(t)) < 80 {
		return false
	}
	// 至少两条编号行，或出现"步骤/计划/方案 + 将/会"这类执行性表述
	numbered, bullet := 0, 0
	for _, ln := range strings.Split(t, "\n") {
		s := strings.TrimSpace(ln)
		if s == "" {
			continue
		}
		if isNumberedLine(s) {
			numbered++
		} else if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") {
			bullet++
		}
	}
	if numbered >= 2 {
		return true
	}
	if bullet >= 3 && (strings.Contains(t, "计划") || strings.Contains(t, "步骤") || strings.Contains(t, "方案")) {
		return true
	}
	return false
}

// LooksLikePlan 供自检与外部判断一段文本是否是改动方案。
func LooksLikePlan(text string) bool { return looksLikePlan(text) }

// isNumberedLine 判断 "1. xxx" / "1)" / "1、" / "步骤1：" 这类行首编号。
func isNumberedLine(s string) bool {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i > 2 {
		return false
	}
	if i >= len(s) {
		return false
	}
	// 逐字节取首字符：数字已确认是 ASCII，后面跟的分隔符可能是多字节的"、"
	rest := []rune(s[i:])
	if len(rest) == 0 {
		return false
	}
	switch rest[0] {
	case '.', ')', ':', '．':
		// "1.5 倍" / "1.5x" 这种小数不算编号
		if rest[0] == '.' && len(rest) > 1 && rest[1] >= '0' && rest[1] <= '9' {
			return false
		}
		return true
	case '、':
		return true
	}
	return false
}

// planTitle 从方案文本里抽一行做卡片标题：优先第一行非空内容。
func planTitle(text string) string {
	for _, ln := range strings.Split(text, "\n") {
		s := strings.TrimSpace(strings.TrimLeft(ln, "#"))
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		r := []rune(s)
		if len(r) > 60 {
			s = string(r[:60]) + "…"
		}
		return s
	}
	return "改动方案"
}

// planStepCount 数一下方案里有多少步，用于卡片上给个规模预期。
func planStepCount(text string) int {
	n := 0
	for _, ln := range strings.Split(text, "\n") {
		s := strings.TrimSpace(ln)
		if isNumberedLine(s) {
			n++
		} else if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") {
			n++
		}
	}
	return n
}

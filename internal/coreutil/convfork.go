package coreutil

import "strings"

// 会话分叉：把当前会话整段复制成新会话，两条线各自往下走、互不影响。
// 典型用法是一个会话里试方案 A，想回到同一出发点再试方案 B，不用重新交代背景。

// ForkTitle 生成分叉会话的标题：原题按 rune 截断，再带一个来源标识。
// 名字太长会把侧栏挤爆，所以先截断；带「分叉」二字是为了在列表里一眼认出来。
func ForkTitle(title, note string) string {
	t := strings.TrimSpace(title)
	if t == "" {
		t = "新对话"
	}
	if r := []rune(t); len(r) > 18 {
		t = string(r[:18]) + "…"
	}
	if n := strings.TrimSpace(note); n != "" {
		return t + " · " + n
	}
	return t + " · 分叉"
}

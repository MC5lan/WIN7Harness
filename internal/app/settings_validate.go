//go:build windows

package app

import (
	"fmt"
	"strconv"
	"strings"
)

// validateSettingsFields 在写入配置前做字段级校验，避免 Sscanf 静默吞掉
// 非法输入后用户以为“保存了但没生效”。只校验非空字段，空值沿用原默认。
func (a *App) validateSettingsFields() (field, msg string) {
	text := func(id string) string { return strings.TrimSpace(a.ui.Field(id, "").String()) }
	intField := func(id, label string, min, max int) (string, string) {
		s := text(id)
		if s == "" {
			return "", ""
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < min || (max > 0 && n > max) {
			if max > 0 {
				return id, fmt.Sprintf("%s 需要是 %d~%d 的整数", label, min, max)
			}
			return id, fmt.Sprintf("%s 需要是不小于 %d 的整数", label, min)
		}
		return "", ""
	}
	floatField := func(id, label string, min, max float64) (string, string) {
		s := text(id)
		if s == "" {
			return "", ""
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil || n < min || (max > 0 && n > max) {
			if max > 0 {
				return id, fmt.Sprintf("%s 需要是 %.0f~%.0f 的数字", label, min, max)
			}
			return id, fmt.Sprintf("%s 需要是不小于 %.0f 的数字", label, min)
		}
		return "", ""
	}

	if id, msg := intField("set.maxtok", "最大输出 token", 1, 0); id != "" {
		return id, msg
	}
	if id, msg := intField("set.budget", "Token 上限", 0, 0); id != "" {
		return id, msg
	}
	if id, msg := intField("set.rounds", "轮数上限", 0, 0); id != "" {
		return id, msg
	}
	if id, msg := intField("set.subto", "子代理超时", 0, 0); id != "" {
		return id, msg
	}
	if id, msg := floatField("set.temp", "温度", 0, 2); id != "" {
		return id, msg
	}
	if id, msg := floatField("set.maxcost", "花费上限", 0, 0); id != "" {
		return id, msg
	}
	if id, msg := floatField("set.pricein", "输入单价", 0, 0); id != "" {
		return id, msg
	}
	if id, msg := floatField("set.priceout", "输出单价", 0, 0); id != "" {
		return id, msg
	}
	if s := text("set.ctxwin"); s != "" {
		if _, ok := parseWindowSize(s); !ok {
			return "set.ctxwin", "上下文窗口需要是 auto、128000、32k、300k、960k 或 1m"
		}
	}
	return "", ""
}

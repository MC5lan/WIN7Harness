package core

import "strings"

// 虚拟键码（与 Win32 一致，但 core 保持跨平台，不依赖 win32 包）。
const (
	vkShift   = 0x10
	vkControl = 0x11
	vkAlt     = 0x12
)

// DefaultKeyBindings 是按键绑定的出厂值；用户配置里缺哪项就用哪项兜底。
func DefaultKeyBindings() map[string]string {
	return map[string]string{
		"palette":       "ctrl+k",
		"sidebar":       "ctrl+b",
		"inspector":     "ctrl+j",
		"find":          "ctrl+f",
		"historySearch": "ctrl+r",
		"newConv":       "ctrl+n",
		"toggleFold":    "ctrl+shift+t",
	}
}

// parseCombo 把 "ctrl+shift+t" 解析为修饰键与主键 VK。
func parseCombo(s string) (ctrl, alt, shift bool, vk int32, ok bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(s)), "+")
	if len(parts) == 0 {
		return false, false, false, 0, false
	}
	key := parts[len(parts)-1]
	for _, p := range parts[:len(parts)-1] {
		switch strings.TrimSpace(p) {
		case "ctrl", "control":
			ctrl = true
		case "alt":
			alt = true
		case "shift":
			shift = true
		}
	}
	switch key {
	case "":
		return false, false, false, 0, false
	case "enter", "return":
		vk = 0x0D
	case "esc", "escape":
		vk = 0x1B
	case "tab":
		vk = 0x09
	case "space":
		vk = 0x20
	case "backspace":
		vk = 0x08
	case "delete":
		vk = 0x2E
	case "up":
		vk = 0x26
	case "down":
		vk = 0x28
	case "left":
		vk = 0x25
	case "right":
		vk = 0x27
	case "home":
		vk = 0x24
	case "end":
		vk = 0x23
	case "pgup", "prior":
		vk = 0x21
	case "pgdn", "next":
		vk = 0x22
	default:
		if len(key) == 1 {
			c := key[0]
			if c >= 'a' && c <= 'z' {
				vk = int32(c - 'a' + 'A')
			} else if c >= '0' && c <= '9' {
				vk = int32(c)
			} else {
				return false, false, false, 0, false
			}
		} else if strings.HasPrefix(key, "f") && len(key) <= 3 {
			n := 0
			for i := 1; i < len(key); i++ {
				if key[i] < '0' || key[i] > '9' {
					return false, false, false, 0, false
				}
				n = n*10 + int(key[i]-'0')
			}
			if n < 1 || n > 24 {
				return false, false, false, 0, false
			}
			vk = int32(0x6F + n) // VK_F1 = 0x70
		} else {
			return false, false, false, 0, false
		}
	}
	return ctrl, alt, shift, vk, true
}

// MatchBinding 判断当前按键是否匹配某动作的绑定。
// mods 由调用方从 GetKeyState 取得（vkShift/vkControl/vkAlt 的布尔值）。
func MatchBinding(action string, cfg map[string]string, vk int32, ctrl, alt, shift bool) bool {
	combo, ok := cfg[action]
	if !ok || strings.TrimSpace(combo) == "" {
		combo = DefaultKeyBindings()[action]
	}
	if combo == "" {
		return false
	}
	c, a, s, key, ok := parseCombo(combo)
	return ok && c == ctrl && a == alt && s == shift && key == vk
}

// ModifierVKs 返回修饰键的 VK 值，供 UI 层查询 GetKeyState。
func ModifierVKs() (shift, ctrl, alt int) { return vkShift, vkControl, vkAlt }

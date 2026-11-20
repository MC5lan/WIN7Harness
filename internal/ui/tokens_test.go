package ui

import "testing"

// TestTokensComplete 保证两套令牌的每个字段都非零。
// 全零会让元素"消失"，是自绘界面最隐蔽的故障，必须在单测层拦住。
// 例外：高对比主题的底板就是纯黑 #000000（COLORREF 0），这是合法值不是漏填，
// 因此对它只要求"文字与强调色非零"。
func TestTokensComplete(t *testing.T) {
	for name, tk := range map[string]Tokens{"dark": DarkTokens(), "light": LightTokens()} {
		vals := map[string]uint32{
			"BGCanvas": tk.BGCanvas, "BGSurface": tk.BGSurface, "BGSurfaceRaise": tk.BGSurfaceRaise,
			"BGSurfaceSunken": tk.BGSurfaceSunken, "BGHover": tk.BGHover, "BGActive": tk.BGActive,
			"BGOverlay": tk.BGOverlay, "BorderHairline": tk.BorderHairline, "BorderStrong": tk.BorderStrong,
			"BorderHighlight": tk.BorderHighlight, "TextPrimary": tk.TextPrimary, "TextSecondary": tk.TextSecondary,
			"TextTertiary": tk.TextTertiary, "TextInverse": tk.TextInverse, "AccentBase": tk.AccentBase,
			"AccentHover": tk.AccentHover, "AccentPressed": tk.AccentPressed, "AccentSubtle": tk.AccentSubtle,
			"StateSuccess": tk.StateSuccess, "StateWarn": tk.StateWarn, "StateDanger": tk.StateDanger,
			"StateInfo": tk.StateInfo, "FocusRing": tk.FocusRing,
		}
		for k, v := range vals {
			if v == 0 {
				t.Errorf("%s 主题的 %s 为零值", name, k)
			}
		}
	}

	// 高对比：纯黑合法，但文字与强调色不能是 0，否则等于看不见。
	hc := HighContrastTokens()
	need := map[string]uint32{
		"TextPrimary": hc.TextPrimary, "TextSecondary": hc.TextSecondary,
		"TextTertiary": hc.TextTertiary, "AccentBase": hc.AccentBase,
		"AccentHover": hc.AccentHover, "AccentPressed": hc.AccentPressed,
		"StateSuccess": hc.StateSuccess, "StateWarn": hc.StateWarn,
		"StateDanger": hc.StateDanger, "FocusRing": hc.FocusRing,
		"BorderHairline": hc.BorderHairline, "BorderStrong": hc.BorderStrong,
	}
	for k, v := range need {
		if v == 0 {
			t.Errorf("hc 主题的 %s 为零值（纯黑只允许出现在底板上）", k)
		}
	}
}

// TestTokensForMatchesTheme 保证 ThemeByName 与 TokensFor 的选择一致，
// 否则会出现"文字用深色、底板用浅色"的错配。
func TestTokensForMatchesTheme(t *testing.T) {
	for _, n := range []string{"light", "dark", "hc", "high-contrast", "unknown"} {
		th := ThemeByName(n)
		got := th.Tokens()
		want := TokensFor(th.Name)
		if got != want {
			t.Errorf("主题 %q 的令牌与 TokensFor(%q) 不一致", n, th.Name)
		}
	}
}

// TestColorRefNotRGB 守住 COLORREF 约定：低字节是红。
// 暖纸令牌用 #RRGGBB 注释书写，抄错就会红蓝互换（紫变橙），这里用已知值钉死。
func TestColorRefNotRGB(t *testing.T) {
	tk := DarkTokens()
	// #E8875B -> R=E8 G=87 B=5B -> 0x5B87E8
	if tk.AccentBase != 0x5B87E8 {
		t.Fatalf("AccentBase 应为 COLORREF 0x5B87E8，实际 0x%06X", tk.AccentBase)
	}
	if tk.BGCanvas != 0x1B1D1F { // #1F1D1B
		t.Fatalf("BGCanvas 应为 COLORREF 0x1B1D1F，实际 0x%06X", tk.BGCanvas)
	}
}

// TestScaledMonotonic 保证缩放单调且 100% 时恒等，避免字号缩放下行高倒挂。
func TestScaledMonotonic(t *testing.T) {
	old := FontScale
	defer func() { FontScale = old }()

	FontScale = 1
	for v := 0; v <= 64; v++ {
		if Scaled(v) != v {
			t.Fatalf("FontScale=1 应恒等，Scaled(%d)=%d", v, Scaled(v))
		}
	}

	for _, s := range []float32{1.25, 1.5, 2.0} {
		FontScale = s
		prev := -1
		for v := 0; v <= 64; v++ {
			got := Scaled(v)
			if got < prev {
				t.Fatalf("FontScale=%.2f 时 Scaled 非单调：v=%d 得 %d，前一值 %d", s, v, got, prev)
			}
			prev = got
		}
	}
}

// TestSpacingGrid 间距必须是 4 的倍数，且圆角不超过上限。
func TestSpacingGrid(t *testing.T) {
	for name, v := range map[string]int{
		"Space1": Space1, "Space2": Space2, "Space3": Space3, "Space4": Space4,
		"Space5": Space5, "Space6": Space6, "Space7": Space7, "Space8": Space8,
	} {
		if v%4 != 0 {
			t.Errorf("%s=%d 不是 4 的倍数", name, v)
		}
	}
	for name, v := range map[string]int{
		"RadiusControl": RadiusControl, "RadiusCard": RadiusCard,
		"RadiusDialog": RadiusDialog, "RadiusMax": RadiusMax,
	} {
		if v <= 0 || v > RadiusMax {
			t.Errorf("%s=%d 超出 (0,%d] 范围", name, v, RadiusMax)
		}
	}
}

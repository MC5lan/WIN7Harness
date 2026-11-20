package ui

import "testing"

// allKinds 是全部组件种类，用于穷举 Resolve。
var allKinds = []ComponentKind{
	KindButtonPrimary, KindButtonSecondary, KindButtonGhost, KindButtonDanger,
	KindInput, KindSelect, KindTextarea, KindToggle,
	KindTab, KindCard, KindListItem, KindBadge, KindBadgeSuccess, KindBadgeWarn, KindBadgeDanger,
	KindTableHeader, KindTableRow, KindScrollbar, KindSplitter, KindStatusBar, KindTooltip,
	KindToastSuccess, KindToastWarn, KindToastDanger, KindDrawer,
}

var allStates = []State{StateDefault, StateHover, StateActive, StateSelected, StateFocus, StateDisabled}

// textlessKinds 是本来就没有文字的组件，它们的 Text 允许为 0。
var textlessKinds = map[ComponentKind]bool{KindToggle: true, KindScrollbar: true, KindSplitter: true}

// TestResolveNeverPanicsAndHasText 穷举组件 × 状态 × 主题：
// 只要 Resolve 能返回一个"文字可读"的样式，界面上就不会出现隐形文字。
func TestResolveNeverPanicsAndHasText(t *testing.T) {
	defer SetTokens(DarkTokens())
	for _, tk := range []struct {
		name string
		set  Tokens
	}{{"dark", DarkTokens()}, {"light", LightTokens()}, {"hc", HighContrastTokens()}} {
		SetTokens(tk.set)
		for _, k := range allKinds {
			for _, s := range allStates {
				sp := Resolve(k, s)
				if textlessKinds[k] {
					continue
				}
				// 文字色本身可以是 0（高对比主题的 TextInverse 就是纯黑，
				// 黑字压在亮黄主按钮上才是可读的）。真正的故障是"文字与底色同色"。
				if sp.Text == sp.Fill && sp.Fill != 0 {
					t.Errorf("%s 主题 %d/%d 的文字与底色相同（0x%06X），会看不见", tk.name, k, s, sp.Fill)
				}
				if sp.Text == 0 && sp.Fill == 0 {
					t.Errorf("%s 主题 %d/%d 文字与底色都为 0", tk.name, k, s)
				}
			}
		}
	}
}

// TestResolveSemantics 钉住几条语义规则，避免以后改令牌时把角色串了。
func TestResolveSemantics(t *testing.T) {
	defer SetTokens(DarkTokens())
	tk := DarkTokens()
	SetTokens(tk)

	if got := Resolve(KindButtonPrimary, StateDefault); got.Fill != tk.AccentBase {
		t.Errorf("主按钮默认底色应为 AccentBase，实际 0x%06X", got.Fill)
	}
	if got := Resolve(KindButtonPrimary, StateHover); got.Fill != tk.AccentHover {
		t.Errorf("主按钮 hover 底色应为 AccentHover，实际 0x%06X", got.Fill)
	}
	if got := Resolve(KindButtonDanger, StateDefault); got.Fill != tk.StateDanger {
		t.Errorf("危险按钮底色应为 StateDanger，实际 0x%06X", got.Fill)
	}
	if got := Resolve(KindInput, StateFocus); got.Border != tk.FocusRing {
		t.Errorf("输入框聚焦描边应为 FocusRing，实际 0x%06X", got.Border)
	}
	if got := Resolve(KindListItem, StateSelected); got.Fill != tk.AccentSubtle {
		t.Errorf("选中行底色应为 AccentSubtle，实际 0x%06X", got.Fill)
	}
	// 禁用态必须退成三级文字，且不允许半透明参与。
	if got := Resolve(KindButtonPrimary, StateDisabled); got.Text != tk.TextTertiary {
		t.Errorf("禁用态文字应为 TextTertiary，实际 0x%06X", got.Text)
	}
}

// TestGalleryRowsCoverStates 保证矩阵页的列与状态枚举同步，
// 漏一列就等于有个状态永远没被验收过。
func TestGalleryRowsCoverStates(t *testing.T) {
	seen := map[State]bool{}
	for _, r := range GalleryRows {
		if seen[r.State] {
			t.Errorf("状态 %d 在矩阵页里重复", r.State)
		}
		seen[r.State] = true
	}
	// 按钮没有 selected 语义（那是列表行/页签的状态），
	// 因此矩阵页的按钮列只要求覆盖除 selected 外的 5 态。
	for _, s := range []State{StateDefault, StateHover, StateActive, StateFocus, StateDisabled} {
		if !seen[s] {
			t.Errorf("状态 %d 未出现在矩阵页的按钮列", s)
		}
	}
	if len(galleryButtons) != 4 {
		t.Errorf("按钮变体应有 4 种，实际 %d", len(galleryButtons))
	}
}

// TestLayoutConstants 守住暖纸设计系统的硬性尺寸（比 Quiet Instrument 更大的触控目标）。
func TestLayoutConstants(t *testing.T) {
	checks := map[string][3]int{
		"ControlH":    {ControlH, 36, 36},
		"ListRowH":    {ListRowH, 36, 36},
		"StatusH":     {StatusH, 28, 28},
		"RailW":       {RailW, 56, 56},
		"InspectorW":  {InspectorW, 280, 380},
		"MinWindowW":  {MinWindowW, 960, 960},
		"MinWindowH":  {MinWindowH, 600, 600},
		"HitMin":      {HitMin, 32, 32},
		"TextareaMin": {TextareaMinH, 80, 80},
	}
	for name, c := range checks {
		if c[0] < c[1] || c[0] > c[2] {
			t.Errorf("%s=%d 超出设计区间 [%d,%d]", name, c[0], c[1], c[2])
		}
	}
	// 字号阶梯必须严格递增：display > h1 > h2 > body >= body.sm > caption
	if !(FSDisplay > FSH1 && FSH1 > FSH2 && FSH2 > FSBody && FSBody >= FSBodySm && FSBodySm > FSCaption) {
		t.Errorf("字号阶梯不单调: %d/%d/%d/%d/%d/%d", FSDisplay, FSH1, FSH2, FSBody, FSBodySm, FSCaption)
	}
}

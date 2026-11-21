package core

import "testing"

// 删除类命令的开关与顺序无关，换个写法不能把高危降级成 safe。
func TestCommandRiskSwitchOrder(t *testing.T) {
	cases := []struct {
		cmd  string
		want RiskLevel
	}{
		// 原有定序条目，行为不变。
		{"rd /s /q x", RiskDanger},
		{"rm -rf /", RiskDanger},
		{`del /f /s /q C:\*`, RiskDanger},
		// 换顺序 / 拆开 / 连写 / 别名。
		{`rd /q /s D:\proj`, RiskDanger},
		{"rd /s/q build", RiskDanger},
		{"RD /Q /S build", RiskDanger},
		{"rmdir /q /s src", RiskDanger},
		{"erase /s /q *", RiskDanger},
		{"del /a /f *.tmp", RiskDanger},
		{"rm -r -f ~", RiskDanger},
		{"rm -f -r build", RiskDanger},
		{"rm -Rf build", RiskDanger},
		{"rm --recursive --force build", RiskDanger},
		{`C:\Windows\System32\cmd.exe /c rd /q /s x`, RiskDanger},
		{"cmd /d /c rmdir /q /s x", RiskDanger},
		{"go test ./... && rd /q /s dist", RiskDanger},
		// 不应误伤。
		{"rd build", RiskSafe},
		{"rm -r build", RiskSafe},
		{"git rm -r -f --cached x", RiskSafe},
		{"echo rd /q /s x", RiskSafe},
		{"del notes.txt", RiskCaution},
		{"echo hello", RiskSafe},
		{"mkdir build", RiskCaution},
	}
	for _, c := range cases {
		if got := CommandRisk(c.cmd); got != c.want {
			t.Errorf("CommandRisk(%q) = %s, want %s", c.cmd, got, c.want)
		}
	}
}

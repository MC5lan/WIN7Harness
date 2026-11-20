package tools

import "testing"

func TestGuardBroadCommand(t *testing.T) {
	block := []string{
		`where /r C:\ WIN7Harness*.exe`,
		`where /r "C:\" foo`,
		`dir /s C:\`,
		`dir C:\ /s`,
		`findstr /s /i foo C:\*.*`,
		`powershell -Command "Get-ChildItem C:\ -Recurse"`,
	}
	for _, cmd := range block {
		if err := guardBroadCommand(cmd); err == nil {
			t.Fatalf("应拒绝全盘命令: %s", cmd)
		}
	}
	allow := []string{
		`where go`,
		`dir /b "D:\proj"`,
		`where /r D:\proj foo`,
		`dir /s /b D:\proj`,
		`Get-ChildItem . -Recurse`,
	}
	for _, cmd := range allow {
		if err := guardBroadCommand(cmd); err != nil {
			t.Fatalf("不应拒绝: %s (%v)", cmd, err)
		}
	}
}

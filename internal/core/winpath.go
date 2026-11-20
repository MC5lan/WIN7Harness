package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var reservedDeviceNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	"CONIN$": true, "CONOUT$": true,
}

// ValidateWindowsPathSyntax 拒绝 Win32 会解释成设备/备用数据流/非法字符的路径。
// 这不是越界检查（EscapesWorkDir 负责），而是防止“看起来是普通文件”的路径
// 实际写到 NUL/CON、NTFS ADS 或带尾随点/空格的另一个文件。
// CaseInsensitiveCollision 检查目录里是否已有仅大小写不同的同名项。
// Windows 文件系统通常大小写不敏感；跨平台仓库里 README.md 与 readme.md
// 在 Linux 可共存，在 Windows 会静默互相覆盖，写前必须挡住。
func CaseInsensitiveCollision(p string) (string, bool) {
	if runtime.GOOS != "windows" {
		return "", false
	}
	dir, base := filepath.Dir(p), filepath.Base(p)
	if base == "" || base == "." || base == ".." {
		return "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.Name() == base {
			return "", false
		}
		if strings.EqualFold(e.Name(), base) {
			return e.Name(), true
		}
	}
	return "", false
}

func ValidateWindowsPathSyntax(p string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	if strings.IndexByte(p, 0) >= 0 {
		return fmt.Errorf("路径包含 NUL 字符")
	}
	check := p
	if strings.HasPrefix(check, `\\?\`) {
		check = check[4:]
		if strings.HasPrefix(strings.ToUpper(check), `GLOBALROOT`) || strings.HasPrefix(strings.ToUpper(check), `PIPE\`) {
			return fmt.Errorf("拒绝设备命名空间路径：%s", p)
		}
	} else if strings.HasPrefix(check, `\\.\`) || strings.HasPrefix(check, `\??\`) {
		return fmt.Errorf("拒绝设备命名空间路径：%s", p)
	}
	vol := filepath.VolumeName(check)
	rest := check[len(vol):]
	// 盘符后的冒号只允许出现在盘符本身；其余冒号按 NTFS ADS 拒绝。
	if strings.Contains(rest, ":") {
		return fmt.Errorf("拒绝 NTFS 备用数据流路径：%s", p)
	}
	if strings.ContainsAny(rest, `*?"<>|`) {
		return fmt.Errorf("路径包含 Windows 非法字符：%s", p)
	}
	for _, seg := range strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' }) {
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		if strings.TrimRight(seg, " .") != seg {
			return fmt.Errorf("路径段以点或空格结尾：%s", seg)
		}
		for _, r := range seg {
			if r < 32 {
				return fmt.Errorf("路径段包含控制字符：%q", seg)
			}
		}
		base := seg
		if i := strings.IndexByte(base, '.'); i >= 0 {
			base = base[:i]
		}
		if reservedDeviceNames[strings.ToUpper(base)] {
			return fmt.Errorf("拒绝 Windows 保留设备名：%s", seg)
		}
	}
	return nil
}

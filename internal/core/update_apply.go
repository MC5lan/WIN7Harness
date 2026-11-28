package core

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// 自更新闭环：v1.28 之前 /update 只做到"下载 + 校验 SHA256"，
// 剩下的替换 exe、重启、清理旧文件全靠人手。
// 这里补上最后一公里，并且刻意保留一条"失败可回滚"的退路：
// 脚本先把自己改名成 .old，新文件搬到位后才真删 .old；
// 中途任何一步失败，脚本会把 .old 改回来，程序仍是能跑的那一版。

// SelfExePath 返回当前可执行文件的绝对路径。
func SelfExePath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

// InstallDirWritable 判断当前 exe 所在目录是否可写。
// 装到 Program Files（所有用户）时普通用户不可写，应用内替换 exe 会失败；
// 升级流程据此改为“请运行新版安装包”。
func InstallDirWritable() bool {
	exe, err := SelfExePath()
	if err != nil {
		return false
	}
	f, err := os.CreateTemp(filepath.Dir(exe), ".wh-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// InstallMode 返回安装形态：standard-user / standard-all / portable。
// 安装器会在程序目录写 install-mode.txt；没有标记时按当前用户标准安装处理。
// 结果在进程内缓存，避免设置页每帧读盘。
var (
	installModeOnce   sync.Once
	installModeCached string
)

func InstallMode() string {
	installModeOnce.Do(func() {
		installModeCached = detectInstallMode()
	})
	return installModeCached
}

func detectInstallMode() string {
	if PortableMode() {
		return "portable"
	}
	if exe, err := SelfExePath(); err == nil {
		if b, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "install-mode.txt")); err == nil {
			switch strings.TrimSpace(string(b)) {
			case "standard-all", "standard-user":
				return strings.TrimSpace(string(b))
			}
		}
	}
	return "standard-user"
}

// ExeArch 从 PE 头读出目标架构，用于拦截"拿错架构的包替换自己"。
// 返回值：amd64 / 386 / arm64 / unknown。
func ExeArch(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// 读 16KB 头：覆盖 DOS stub 较大的合法 PE（部分数字签名/GUI 包的 stub 超过 4KB）
	head := make([]byte, 16384)
	n, err := f.Read(head)
	if err != nil && n < 64 {
		return "", err
	}
	head = head[:n]
	if len(head) < 0x40 || head[0] != 'M' || head[1] != 'Z' {
		return "", errors.New("不是有效的 PE 文件：缺少 MZ 头")
	}
	off := int(binary.LittleEndian.Uint32(head[0x3c:0x40]))
	if off <= 0 || off+24 > len(head) {
		return "", errors.New("PE 头偏移越界，文件可能已损坏")
	}
	if head[off] != 'P' || head[off+1] != 'E' || head[off+2] != 0 || head[off+3] != 0 {
		return "", errors.New("不是有效的 PE 文件：缺少 PE 签名")
	}
	machine := binary.LittleEndian.Uint16(head[off+4 : off+6])
	switch machine {
	case 0x8664:
		return "amd64", nil
	case 0x014c:
		return "386", nil
	case 0xaa64:
		return "arm64", nil
	default:
		return "unknown", nil
	}
}

// CheckUpdateArch 校验待替换的 exe 与当前进程架构一致。
func CheckUpdateArch(newExe, wantArch string) error {
	if wantArch == "" {
		wantArch = runtime.GOARCH
	}
	got, err := ExeArch(newExe)
	if err != nil {
		return err
	}
	if got == "unknown" {
		return errors.New("无法识别新包的架构，为安全起见不替换")
	}
	if got != wantArch {
		return fmt.Errorf("架构不匹配：当前 %s，新包 %s。换错架构的程序会直接起不来", wantArch, got)
	}
	return nil
}

// applyWaitTries 是脚本等待旧进程退出的最大重试次数（每次约 1 秒）。
const applyWaitTries = 30

// updateHealthFile 是新版本启动成功后写下的健康标记。
// 替换脚本在启动新版本后等待这个文件；60 秒内没出现就自动回滚。
const updateHealthFile = "update-healthy.txt"

// UpdateHealthPath 返回健康标记文件路径（与替换脚本共用）。
func UpdateHealthPath() string { return filepath.Join(LocalDataDir(), updateHealthFile) }

// MarkUpdateHealthy 由新版本在主循环稳定运行几秒后调用。
// 写入版本与时间，便于替换脚本判断“新版本真的起来了”。
func MarkUpdateHealthy() error {
	p := UpdateHealthPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data := []byte(Version + "\r\n" + time.Now().Format(time.RFC3339) + "\r\n")
	return WriteFileAtomic(p, data, 0o644)
}

// ApplyScript 生成 Windows 替换脚本。
// 不用 PowerShell（Win7 上可能被策略拦），只用 cmd 内建命令 + ping 做退避。
func ApplyScript(exePath, newExe string, relaunch bool) string {
	var b strings.Builder
	b.WriteString("@echo off\r\n")
	b.WriteString("setlocal\r\n")
	// set "VAR=value" 是 cmd 里唯一安全的带引号赋值：
	// 整个 VAR=value 被引号包住，值里不再需要二次引号，
	// 含空格路径（C:\Program Files\...）下替换成功；也不受 % 展开干扰。
	b.WriteString("set \"TARGET=" + batValue(exePath) + "\"\r\n")
	b.WriteString("set \"NEW=" + batValue(newExe) + "\"\r\n")
	b.WriteString("set \"OLD=%TARGET%.old\"\r\n")
	b.WriteString("set \"HEALTH=" + batValue(UpdateHealthPath()) + "\"\r\n")
	b.WriteString("set \"NAME=" + batValue(filepath.Base(exePath)) + "\"\r\n")
	b.WriteString("set TRIES=0\r\n")
	b.WriteString(":wait\r\n")
	b.WriteString("move /y \"%TARGET%\" \"%OLD%\" >nul 2>&1\r\n")
	b.WriteString("if not errorlevel 1 goto swap\r\n")
	b.WriteString("set /a TRIES+=1\r\n")
	b.WriteString(fmt.Sprintf("if %%TRIES%% geq %d goto giveup\r\n", applyWaitTries))
	b.WriteString("ping -n 2 127.0.0.1 >nul\r\n")
	b.WriteString("goto wait\r\n")
	b.WriteString(":swap\r\n")
	b.WriteString("move /y \"%NEW%\" \"%TARGET%\" >nul 2>&1\r\n")
	b.WriteString("if errorlevel 1 goto rollback\r\n")
	if relaunch {
		// 先清掉上一次的标记，再启动新版本；新版本稳定运行 3 秒后写入。
		b.WriteString("del \"%HEALTH%\" >nul 2>&1\r\n")
		b.WriteString("start \"\" \"%TARGET%\"\r\n")
		b.WriteString("set WAIT=0\r\n")
		b.WriteString(":health\r\n")
		b.WriteString("if exist \"%HEALTH%\" goto healthy\r\n")
		b.WriteString("set /a WAIT+=1\r\n")
		b.WriteString("if %WAIT% geq 60 goto healthfail\r\n")
		b.WriteString("ping -n 2 127.0.0.1 >nul\r\n")
		b.WriteString("goto health\r\n")
		b.WriteString(":healthy\r\n")
		b.WriteString("del \"%HEALTH%\" >nul 2>&1\r\n")
		b.WriteString(":healthfail\r\n")
		b.WriteString("taskkill /f /im \"%NAME%\" >nul 2>&1\r\n")
		b.WriteString("move /y \"%OLD%\" \"%TARGET%\" >nul 2>&1\r\n")
		b.WriteString("start \"\" \"%TARGET%\"\r\n")
		b.WriteString("echo 新版本未在 60 秒内完成启动，已自动回滚到原版本。 >\"%~dp0update_failed.txt\"\r\n")
		b.WriteString("del \"%~f0\" >nul 2>&1\r\n")
		b.WriteString("exit /b 3\r\n")
	}
	b.WriteString("del \"%~f0\" >nul 2>&1\r\n")
	b.WriteString("exit /b 0\r\n")
	b.WriteString(":rollback\r\n")
	b.WriteString("move /y \"%OLD%\" \"%TARGET%\" >nul 2>&1\r\n")
	b.WriteString("echo 替换失败，已回滚到原版本。 >\"%~dp0update_failed.txt\"\r\n")
	b.WriteString("del \"%~f0\" >nul 2>&1\r\n")
	b.WriteString("exit /b 1\r\n")
	b.WriteString(":giveup\r\n")
	b.WriteString("echo 替换脚本没有能移动文件（程序占用或路径含特殊字符），原版本未受影响。 >\"%~dp0update_failed.txt\"\r\n")
	b.WriteString("del \"%~f0\" >nul 2>&1\r\n")
	b.WriteString("exit /b 2\r\n")
	return b.String()
}

// batValue 给 batch set 的值做转义：去掉引号、% 翻倍
// （batch 里 %% 才表示一个字面量 %）。
func batValue(p string) string {
	p = strings.ReplaceAll(p, "\"", "")
	p = strings.ReplaceAll(p, "%", "%%")
	return p
}

// WriteApplyScript 把替换脚本写进 dir，返回脚本绝对路径。
func WriteApplyScript(dir, script string) (string, error) {
	if dir == "" {
		dir = UpdatesDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("apply-%d.bat", time.Now().UnixNano())
	p := filepath.Join(dir, name)
	if err := WriteFileAtomic(p, []byte(script), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// CleanupOldExe 清理上一版 .old（启动时调用）。
// 关键修复（CR-P1-2）：此前**无条件删除**——新版首次启动就把唯一的回滚副本删了，
// 换上的包要是起不来，用户就再无退路。现在无条件保留最新一份 .old，
// 其余超过 30 天的才清理。注意不能只看 .old 的 mtime：Windows 同卷 move
// 会保留原 exe 的写入时间，上一版可能是几个月前安装的，按 mtime 会把唯一
// 回滚副本误删。
func CleanupOldExe(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	self, err := SelfExePath()
	if err != nil {
		return 0
	}
	base := filepath.Base(self)
	type oldFile struct {
		name  string
		mtime time.Time
	}
	var olds []oldFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, base) || !strings.HasSuffix(name, ".old") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		olds = append(olds, oldFile{name: name, mtime: info.ModTime()})
	}
	if len(olds) == 0 {
		return 0
	}
	sort.Slice(olds, func(i, j int) bool { return olds[i].mtime.After(olds[j].mtime) })
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	removed := 0
	for i, f := range olds {
		if i == 0 {
			continue // 最新一份永远保留，作为回滚副本
		}
		if f.mtime.After(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(dir, f.name)) == nil {
			removed++
		}
	}
	return removed
}

// PendingUpdate 读走上次替换失败留下的标记（update_failed.txt），读后即删。
func PendingUpdate(dir string) string {
	if dir == "" {
		dir = UpdatesDir()
	}
	p := filepath.Join(dir, "update_failed.txt")
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	_ = os.Remove(p)
	return strings.TrimSpace(string(b))
}

// PrepareUpdate 准备更新目录：目录不存在就建。
// 同一版本的包会被下一次下载直接覆盖，不会无限堆积。
func PrepareUpdate(dir string) error {
	if dir == "" {
		dir = UpdatesDir()
	}
	return os.MkdirAll(dir, 0o755)
}

// ExeArchOfSelf 当前程序自身的架构（386 / amd64）。
func ExeArchOfSelf() string {
	self, err := SelfExePath()
	if err != nil {
		return ""
	}
	arch, err := ExeArch(self)
	if err != nil {
		return ""
	}
	return arch
}

// StageUpdate 把"已下载待替换"的包登记下来，下次启动若发现还没换成功可以提示。
func StageUpdate(dir, newExe string) error {
	if dir == "" {
		dir = UpdatesDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return WriteFileAtomic(filepath.Join(dir, "pending_update.txt"), []byte(newExe), 0o644)
}

// PendingUpdateExe 读回上次登记的待替换包路径（不存在返回空串）。
func PendingUpdateExe(dir string) string {
	if dir == "" {
		dir = UpdatesDir()
	}
	b, err := os.ReadFile(filepath.Join(dir, "pending_update.txt"))
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(b))
	if !fileExists(p) {
		return ""
	}
	return p
}

// LaunchApply 用 cmd 启动替换脚本。脚本自己会等本进程退出再动手，
// 所以这里不阻塞、也不关心返回值。
func LaunchApply(script string) error {
	if script == "" {
		return errors.New("脚本路径为空")
	}
	cmd := exec.Command("cmd", "/c", "start", "", script)
	cmd.SysProcAttr = hiddenProcAttr()
	return cmd.Start()
}

// RevertInfo 描述一次可回退的状态（当前版本之前的那一版）。
type RevertInfo struct {
	OldPath string
	OldArch string
	OldSize int64
	OldMod  time.Time
}

// PrepareRevert 检查回退前置条件：当前 exe 旁边有 .old，且它是合法 PE。
// .old 是更新器保留的上一版（<exe>.old），30 天后由 CleanupOldExe 自动清理。
func PrepareRevert() (RevertInfo, error) {
	self, err := SelfExePath()
	if err != nil {
		return RevertInfo{}, fmt.Errorf("拿不到自身路径：%v", err)
	}
	return PrepareRevertFrom(self)
}

// PrepareRevertFrom 分离出来便于测试：从任意 exe 路径算出对应 .old。
func PrepareRevertFrom(exePath string) (RevertInfo, error) {
	oldPath := exePath + ".old"
	fi, err := os.Stat(oldPath)
	if err != nil {
		return RevertInfo{}, fmt.Errorf("没有可回退的版本：上一版 %s 不存在（更新成功后更新器会保留 30 天，之后自动清理）", filepath.Base(oldPath))
	}
	if fi.Size() < 1<<20 {
		return RevertInfo{}, fmt.Errorf("回滚副本异常（仅 %d 字节），放弃回退", fi.Size())
	}
	arch, err := ExeArch(oldPath)
	if err != nil {
		return RevertInfo{}, fmt.Errorf("回滚副本不是有效的 Windows PE 文件：%v", err)
	}
	return RevertInfo{OldPath: oldPath, OldArch: arch, OldSize: fi.Size(), OldMod: fi.ModTime()}, nil
}

// RevertScript 生成“回退到 .old 版本”替换脚本。
// 思路与 ApplyScript 相同：等本进程退出，把当前 exe 临时改名为 .new，
// 把 .old 改名回原路径，然后重启；失败时把 .new 改回当前版本，
// 保证任何时候都已知自己跑的是哪个版本（不会变砖）。
func RevertScript(exePath string, relaunch bool) string {
	var b strings.Builder
	b.WriteString("@echo off\r\n")
	b.WriteString("setlocal\r\n")
	b.WriteString("set \"TARGET=" + batValue(exePath) + "\"\r\n")
	b.WriteString("set \"OLD=%TARGET%.old\"\r\n")
	b.WriteString("set \"NEW=%TARGET%.new\"\r\n")
	b.WriteString("set TRIES=0\r\n")
	b.WriteString(":wait\r\n")
	b.WriteString("move /y \"%TARGET%\" \"%NEW%\" >nul 2>&1\r\n")
	b.WriteString("if not errorlevel 1 goto swap\r\n")
	b.WriteString("set /a TRIES+=1\r\n")
	b.WriteString(fmt.Sprintf("if %%TRIES%% geq %d goto giveup\r\n", applyWaitTries))
	b.WriteString("ping -n 2 127.0.0.1 >nul\r\n")
	b.WriteString("goto wait\r\n")
	b.WriteString(":swap\r\n")
	b.WriteString("move /y \"%OLD%\" \"%TARGET%\" >nul 2>&1\r\n")
	b.WriteString("if errorlevel 1 goto rollback\r\n")
	if relaunch {
		b.WriteString("start \"\" \"%TARGET%\"\r\n")
	}
	b.WriteString("del \"%~f0\" >nul 2>&1\r\n")
	b.WriteString("exit /b 0\r\n")
	b.WriteString(":rollback\r\n")
	b.WriteString("move /y \"%NEW%\" \"%TARGET%\" >nul 2>&1\r\n")
	b.WriteString("echo 回退失败，已回滚到当前版本。 >\"%~dp0update_failed.txt\"\r\n")
	b.WriteString("del \"%~f0\" >nul 2>&1\r\n")
	b.WriteString("exit /b 1\r\n")
	b.WriteString(":giveup\r\n")
	b.WriteString("echo 回退脚本没有能移动文件，程序保持现状。 >\"%~dp0update_failed.txt\"\r\n")
	b.WriteString("del \"%~f0\" >nul 2>&1\r\n")
	b.WriteString("exit /b 2\r\n")
	return b.String()
}

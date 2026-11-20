//go:build windows

package core

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// renameAtomic 在 Windows 上对杀毒软件/索引器/共享盘短暂占用做有限重试。
// Win7 资源管理器缩略图、360/MSE 扫描都可能让 MoveFileEx 返回
// ERROR_SHARING_VIOLATION/ERROR_ACCESS_DENIED；直接报错会让用户以为文件坏了。
func renameAtomic(oldPath, newPath string) error {
	const (
		errSharingViolation = syscall.Errno(32)
		errLockViolation    = syscall.Errno(33)
		errAccessDenied     = syscall.Errno(5)
		errNotReady         = syscall.Errno(21)
		errBadNetPath       = syscall.Errno(53)
		errNetBusy          = syscall.Errno(54)
		errUnexpNetErr      = syscall.Errno(59)
		errNetnameDeleted   = syscall.Errno(64)
		errBusy             = syscall.Errno(170)
	)
	var last error
	for i := 0; i < 8; i++ {
		err := os.Rename(oldPath, newPath)
		if err == nil {
			return nil
		}
		last = err
		if !errors.Is(err, errSharingViolation) &&
			!errors.Is(err, errLockViolation) &&
			!errors.Is(err, errAccessDenied) &&
			!errors.Is(err, errNotReady) &&
			!errors.Is(err, errBadNetPath) &&
			!errors.Is(err, errNetBusy) &&
			!errors.Is(err, errUnexpNetErr) &&
			!errors.Is(err, errNetnameDeleted) &&
			!errors.Is(err, errBusy) {
			return err
		}
		time.Sleep(time.Duration(i+1) * 30 * time.Millisecond)
	}
	return last
}

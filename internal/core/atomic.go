package core

import (
	"os"
	"path/filepath"
)

// 原子写：先写同目录临时文件，落盘后再整体替换。
//
// 为什么必须这么做：Win7 上 os.WriteFile 是先截断再写。若写到一半磁盘满、
// 进程被杀、断电，原文件就变成半截（甚至 0 字节），用户的心血没了。
// 改成"临时文件 + os.Rename 替换"后，失败最多留下一个临时文件，原文件完好。
//
// 注意临时文件必须和目标同目录（跨盘 Rename 会失败）。
// 原文件的只读位会被保留（只读配置重写后不该被悄悄解开）；
// 原文件的 DACL/所有者不保留——那需要 GetFileSecurity/SetFileSecurity 双向复制，
// 失败路径比原子写本身还复杂，这里只做保守兜底：写入永远不因 ACL 失败而失败。
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	readonly := false
	if st, err := os.Stat(path); err == nil {
		readonly = st.Mode().Perm()&0o200 == 0
	}
	tmp, err := os.CreateTemp(dir, ".~w7h-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	// Win7 上不 Sync 的话，Rename 成功后掉电仍可能丢内容
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		// 权限设置失败不致命，Win7 上通常直接忽略
		_ = err
	}
	if err := renameAtomic(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	if readonly {
		// 目标原本只读：把新写的文件也置回只读（用户显式设的保护，不因写入消失）
		_ = os.Chmod(path, perm&^0o200)
	}
	return nil
}

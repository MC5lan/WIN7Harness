package core

import (
	"io"
	"os"
	"path/filepath"
)

// 旧版本把所有数据都放在漫游 %APPDATA%\WIN7Harness。域环境下这会让
// 登录/注销同步几百 MB 会话与索引。这里在首次升级时把大目录迁到
// %LOCALAPPDATA%，配置/技能/命令等小文件仍留在漫游目录。
//
// 迁移只在目标不存在时发生；跨盘（漫游目录在网络盘）rename 失败则复制。
// 失败只记日志，不阻断启动——旧数据仍在原位置，用户至少不会丢。
func MigrateLocalData() {
	oldRoot, newRoot := DataDir(), LocalDataDir()
	if samePath(oldRoot, newRoot) {
		return
	}
	for _, name := range []string{
		"sessions",
		"snapshots",
		"crash",
		"updates",
		"selftest",
		"attachments",
		"worktrees",
		"eval",
		"exports",
		"codeindex.json",
		"harness.kv.db",
		"last_run.json",
		"remote-cert.pem",
	} {
		src := filepath.Join(oldRoot, name)
		dst := filepath.Join(newRoot, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			Logf("localdata: 创建目标目录失败 %s：%v", filepath.Dir(dst), err)
			continue
		}
		if err := os.Rename(src, dst); err == nil {
			Logf("localdata: 已迁移 %s → %s", src, dst)
			continue
		}
		if err := copyPath(src, dst); err != nil {
			_ = os.RemoveAll(dst)
			Logf("localdata: 迁移 %s 失败（保留旧位置）：%v", src, err)
			continue
		}
		_ = os.RemoveAll(src)
		Logf("localdata: 已复制迁移 %s → %s", src, dst)
	}
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

func copyPath(src, dst string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if st.IsDir() {
		if err := os.MkdirAll(dst, st.Mode().Perm()); err != nil {
			return err
		}
		ents, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if err := copyPath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !st.Mode().IsRegular() {
		// 会话/缓存里不应有链接；遇到就跳过，不把旧路径的链接语义带过来。
		return nil
	}
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

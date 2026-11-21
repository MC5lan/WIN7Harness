//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"win7harness/internal/core"
)

// 本文件由 commands.go 的 handleSlash 拆分而来（T08），仅移动 case body，未改行为。
// Git 与项目：/init /commit /gitstatus /worktree。

// slashInit 处理 /init 及别名。
func (a *App) slashInit(rest string) bool {
	if a.eng == nil {
		return true
	}
	wd := a.eng.WorkDir
	p := filepath.Join(wd, "AGENTS.md")
	if _, err := os.Stat(p); err == nil {
		a.sysNote("已存在 AGENTS.md，未覆盖。可直接编辑该文件补充项目约定。")
		return true
	}
	if err := core.WriteFileAtomic(p, []byte(core.ProjectDocSkeleton(wd)), 0o644); err != nil {
		a.toast("写入失败："+err.Error(), true)
		return true
	}
	a.sysNote("已生成项目约定文件：\n" + p + "\n\n补充构建命令、代码风格等约定后，之后每轮对话会自动加载。")
	return true
}

// slashCommit 处理 /commit 及别名。
func (a *App) slashCommit(rest string) bool {
	if a.eng == nil {
		return true
	}
	wd := a.eng.WorkDir
	if !core.IsGitRepo(wd) {
		a.sysNote("当前工作目录不是 git 仓库，无法提交：\n" + wd +
			"\n\n想做版本管理可先执行：git init && git add -A && git commit -m init")
		return true
	}
	msg, err := core.CommitAll(wd, strings.TrimSpace(rest))
	if err != nil {
		a.sysNote("提交未完成：" + err.Error())
		return true
	}
	a.sysNote(msg)
	return true
}

// slashGitStatus 处理 /gitstatus 及别名。
func (a *App) slashGitStatus(rest string) bool {
	if a.eng == nil {
		return true
	}
	wd := a.eng.WorkDir
	if !core.IsGitRepo(wd) {
		a.sysNote("不是 git 仓库：" + wd)
		return true
	}
	var b strings.Builder
	if st := core.GitState(wd); st != "" {
		b.WriteString(st + "\n")
	}
	changes := core.GitChanges(wd)
	if len(changes) == 0 {
		b.WriteString("工作区干净。\n")
	} else {
		fmt.Fprintf(&b, "共 %d 处改动：\n", len(changes))
		for _, c := range changes {
			fmt.Fprintf(&b, "  [%s] %s\n", c.Stage(), c.Path)
		}
		if block, notes := core.GitPreflight(wd); block != "" {
			b.WriteString("\n预检未通过：" + block + "\n")
		} else if len(notes) > 0 {
			b.WriteString("\n预检提示：\n")
			for _, n := range notes {
				b.WriteString("  - " + n + "\n")
			}
		}
	}
	a.sysNote(b.String())
	return true
}

// slashWorktree 处理 /worktree 及别名。
func (a *App) slashWorktree(rest string) bool {
	a.cmdWorktree(rest)
	return true
}

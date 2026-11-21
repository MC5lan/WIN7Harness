//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"win7harness/internal/core"
)

// /worktree：把当前会话切到 git worktree 隔离副本里。
//
// 大型重构最怕“改到一半主工作区不能编译”。worktree 从 HEAD 开一个独立分支，
// Agent 在副本里改、跑测试；主工作区保持干净，验证通过后再由用户合并。
// 这不是自动合并工具：merge 仍交给 git，避免程序替用户决定冲突怎么解。
func (a *App) cmdWorktree(rest string) {
	cv := a.currentConv()
	if cv == nil {
		a.sysNote("当前没有会话。")
		return
	}
	fields := strings.Fields(rest)
	sub := "status"
	if len(fields) > 0 {
		sub = strings.ToLower(fields[0])
	}
	baseDir := a.convWorkDir()

	switch sub {
	case "status", "":
		a.worktreeStatus(cv, baseDir)
	case "new", "create":
		a.worktreeNew(cv, fields, baseDir)
	case "remove", "delete":
		a.worktreeRemove(cv)
	case "merge":
		a.worktreeMerge(cv)
	case "list":
		a.worktreeList(cv, baseDir)
	default:
		a.sysNote("用法：\n- `/worktree` 查看状态\n- `/worktree new [分支名]` 从 HEAD 创建隔离副本\n- `/worktree merge` 预检 + 合并回原仓库，冲突自动 abort\n- `/worktree list` 列出 git worktree\n- `/worktree remove` 删除当前隔离副本，回到原仓库")
	}
}

func (a *App) worktreeStatus(cv *Conv, baseDir string) {
	var b strings.Builder
	b.WriteString("当前工作目录：`" + baseDir + "`\n")
	if cv.WorktreeFrom != "" {
		b.WriteString("隔离状态：**已启用 worktree**\n")
		b.WriteString("原仓库：`" + cv.WorktreeFrom + "`\n")
		b.WriteString("隔离分支：`" + cv.WorktreeBranch + "`\n")
		b.WriteString("退出隔离：`/worktree remove`（未提交改动会丢失）")
	} else {
		b.WriteString("隔离状态：普通目录\n")
		b.WriteString("开启：`/worktree new`（从 HEAD 建隔离分支）；自定义分支名：`/worktree new feature/x`\n")
		b.WriteString("查看 git 列表：`/worktree list`")
	}
	a.sysNote(b.String())
}

func (a *App) worktreeNew(cv *Conv, fields []string, baseDir string) {
	if cv.WorktreeFrom != "" {
		a.sysNote("当前会话已在 worktree 隔离中：`" + baseDir + "`\n如需重建，先 `/worktree remove`。")
		return
	}
	if !core.GitAvailable() {
		a.sysNote("没有找到 git，无法创建 worktree。请先安装 git，或在普通目录里工作。")
		return
	}
	root, err := gitRoot(baseDir)
	if err != nil {
		a.sysNote("当前工作目录不是 git 仓库（或不在仓库内）：\n" + baseDir + "\n\n先 `git init` / `git clone` 再试。")
		return
	}
	branch := "harness/" + cv.ID
	if len(fields) > 1 {
		branch = fields[1]
	}
	dir := filepath.Join(core.LocalDataDir(), "worktrees", cv.ID)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		a.sysNote("创建 worktrees 目录失败：" + err.Error())
		return
	}
	if _, err := os.Stat(dir); err == nil {
		a.sysNote("worktree 目录已存在：\n" + dir + "\n如需继续使用它，请手动切换工作目录；或先删除后重试。")
		return
	}
	out, err := createWorktree(root, branch, dir)
	if err != nil {
		a.sysNote("git worktree add 失败：\n" + strings.TrimSpace(out) + "\n" + err.Error())
		return
	}
	cv.WorktreeFrom = root
	cv.WorktreeBranch = branch
	cv.WorkDir = dir
	a.saveConv()
	a.rebuildEngine()
	a.sysNote(fmt.Sprintf("已创建 worktree 隔离：\n- 原仓库：`%s`\n- 隔离目录：`%s`\n- 分支：`%s`\n\n"+
		"之后的改动、测试都在副本里；主工作区不受影响。\n完成后用 `/worktree remove` 回到原仓库（未提交改动会丢失；需要保留就先用 git commit）。",
		root, dir, branch))
}

func (a *App) worktreeRemove(cv *Conv) {
	if cv.WorktreeFrom == "" {
		a.sysNote("当前会话不在 worktree 隔离中。")
		return
	}
	path := cv.WorkDir
	root := cv.WorktreeFrom
	if !a.confirm("退出 worktree 隔离？", "将删除隔离目录：\n"+path+"\n\n未提交的改动会丢失。已提交的改动保留在分支 "+cv.WorktreeBranch+" 上。") {
		return
	}
	out, err := core.GitRun(root, "worktree", "remove", "--force", path)
	if err != nil {
		a.sysNote("git worktree remove 失败：\n" + strings.TrimSpace(out) + "\n" + err.Error() + "\n\n可手动删除目录后继续使用原仓库。")
		return
	}
	cv.WorkDir = root
	cv.WorktreeFrom = ""
	cv.WorktreeBranch = ""
	a.saveConv()
	a.rebuildEngine()
	a.sysNote("已退出 worktree 隔离，回到原仓库：`" + root + "`")
}

func (a *App) worktreeMerge(cv *Conv) {
	if cv.WorktreeFrom == "" {
		a.sysNote("当前会话不在 worktree 隔离中。")
		return
	}
	root, wt, branch := cv.WorktreeFrom, cv.WorkDir, cv.WorktreeBranch
	// 主仓库必须是干净的，否则 merge 冲突时无法安全 abort 回原状态。
	if dirty, files := gitDirty(root); dirty {
		a.sysNote("原仓库有未提交改动，先处理后再合并：\n\n" + strings.Join(files, "\n") + "\n\n建议先 commit 或 stash，避免 merge 失败时把两边改动搅在一起。")
		return
	}
	if dirty, files := gitDirty(wt); dirty {
		preview := "隔离副本有未提交改动：\n" + strings.Join(files, "\n") + "\n\n合并前会先提交这些改动（作者 WIN7Harness）。继续？"
		if !a.confirm("合并 worktree 前提交改动？", preview) {
			return
		}
		if out, err := commitAll(wt, "harness: "+cv.Title); err != nil {
			a.sysNote("隔离副本提交失败：\n" + strings.TrimSpace(out) + "\n" + err.Error())
			return
		}
	}
	stat, _ := core.GitRun(root, "diff", "--stat", "HEAD..."+branch)
	logText, _ := core.GitRun(root, "log", "--oneline", "-5", branch)
	review := "原仓库：" + root + "\n隔离分支：" + branch + "\n\n" + strings.TrimSpace(stat) + "\n\n最近提交：\n" + strings.TrimSpace(logText) + "\n\n合并采用 --no-ff；若冲突会自动 git merge --abort，主仓库不会留下半合并状态。继续？"
	if !a.confirm("合并 worktree 到原仓库？", review) {
		return
	}
	out, err := core.GitRun(root, "-c", "user.name=WIN7Harness", "-c", "user.email=win7harness@localhost",
		"merge", "--no-ff", "--no-edit", branch)
	if err != nil {
		conflicts, _ := core.GitRun(root, "diff", "--name-only", "--diff-filter=U")
		_, _ = core.GitRun(root, "merge", "--abort")
		a.sysNote("合并失败，已自动回滚主仓库（git merge --abort）。\n\n冲突文件：\n" + strings.TrimSpace(conflicts) + "\n\n" + strings.TrimSpace(out))
		return
	}
	// merge 成功：隔离副本已无存在必要，删除目录但保留分支，方便用户查看/回滚。
	_, _ = core.GitRun(root, "worktree", "remove", "--force", wt)
	cv.WorkDir = root
	cv.WorktreeFrom = ""
	cv.WorktreeBranch = ""
	a.saveConv()
	a.rebuildEngine()
	a.sysNote("已合并 `" + branch + "` 到原仓库，并删除隔离目录。\n分支仍保留，可用 `git log " + branch + "` 查看；需要回滚就 `git reset --hard HEAD^`（确认后再执行）。")
}

func (a *App) worktreeList(cv *Conv, baseDir string) {
	showDir := cv.WorktreeFrom
	if showDir == "" {
		showDir = baseDir
	}
	root, err := gitRoot(showDir)
	if err != nil {
		a.sysNote("当前目录不是 git 仓库。")
		return
	}
	out, err := core.GitRun(root, "worktree", "list")
	if err != nil {
		a.sysNote("git worktree list 失败：\n" + strings.TrimSpace(out) + "\n" + err.Error())
		return
	}
	a.sysNote("```\n" + strings.TrimRight(out, "\n") + "\n```")
}

func gitDirty(dir string) (bool, []string) {
	out, err := core.GitRun(dir, "status", "--porcelain")
	if err != nil {
		return false, nil
	}
	var files []string
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		files = append(files, line)
		if len(files) >= 20 {
			files = append(files, "…")
			break
		}
	}
	return len(files) > 0, files
}

func commitAll(dir, msg string) (string, error) {
	if out, err := core.GitRun(dir, "add", "-A"); err != nil {
		return out, err
	}
	if strings.TrimSpace(msg) == "" {
		msg = "harness: worktree changes"
	}
	return core.GitRun(dir, "-c", "user.name=WIN7Harness", "-c", "user.email=win7harness@localhost",
		"commit", "-m", msg)
}

func createWorktree(root, branch, dir string) (string, error) {
	out, err := core.GitRun(root, "worktree", "add", "-b", branch, dir, "HEAD")
	if err == nil {
		return out, nil
	}
	// 分支已存在时 -b 会失败；退回挂到已有分支。
	out2, err2 := core.GitRun(root, "worktree", "add", dir, branch)
	if err2 != nil {
		return out + "\n" + out2, err2
	}
	return out2, nil
}

func gitRoot(dir string) (string, error) {
	out, err := core.GitRun(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", fmt.Errorf("empty git root")
	}
	return root, nil
}

package evolve

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cmdResult 保存一次外部命令的输出与退出码。
// stdout / stderr 分开保存：evalrun 的 -json 只往 stdout 写 JSON，
// 编译错误在 stderr，混在一起会解析失败。
type cmdResult struct {
	Stdout string
	Stderr string
	Code   int
}

func runCmd(ctx context.Context, dir string, timeout time.Duration, env []string, name string, args ...string) (cmdResult, error) {
	cctx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		cctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	// 刻意不用 exec.CommandContext：它在超时时只杀直接子进程，
	// go test 派生的 test.exe 会成为孤儿。这里自己收整棵树。
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return cmdResult{}, err
	}
	// go test 会再派生 test.exe；只杀 go 进程会留下孤儿测试进程，
	// 它们继续占 CPU 和文件锁，下一轮门禁会莫名变慢。超时/取消时收整棵树。
	done := make(chan struct{})
	go func(pid int) {
		select {
		case <-cctx.Done():
			killProcessTree(pid)
		case <-done:
		}
	}(cmd.Process.Pid)
	err := cmd.Wait()
	close(done)
	res := cmdResult{Stdout: out.String(), Stderr: errb.String()}
	if err == nil {
		return res, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		res.Code = ee.ExitCode()
		if cctx.Err() != nil {
			return res, fmt.Errorf("%s %s 超时：%w", name, strings.Join(args, " "), cctx.Err())
		}
		return res, fmt.Errorf("%s %s 失败（退出码 %d）：%s", name, strings.Join(args, " "), res.Code, clip(res.Stderr, 800))
	}
	return res, err
}

// gitOut 执行 git 并返回 trim 后的 stdout；失败时把 stderr 带进错误。
func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := runCmd(ctx, dir, 60*time.Second, nil, "git", args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Stdout), nil
}

func isGitRepo(ctx context.Context, dir string) bool {
	out, err := gitOut(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

func branchExists(ctx context.Context, repo, branch string) bool {
	_, err := gitOut(ctx, repo, "rev-parse", "--verify", "refs/heads/"+branch)
	return err == nil
}

func ensureBranch(ctx context.Context, repo, branch, from string) error {
	if branchExists(ctx, repo, branch) {
		return nil
	}
	if !branchExists(ctx, repo, from) {
		return fmt.Errorf("起点分支不存在：%s", from)
	}
	_, err := gitOut(ctx, repo, "branch", branch, from)
	if err != nil {
		return fmt.Errorf("创建分支 %s（来自 %s）失败：%w", branch, from, err)
	}
	return nil
}

func statusPorcelain(ctx context.Context, dir string) (string, error) {
	return gitOut(ctx, dir, "status", "--porcelain")
}

// prepareWorktree 在 workRoot/name 准备一个干净 worktree。
// create 为 true 时新建分支；否则检出已有分支；startPoint 为空表示 HEAD。
func prepareWorktree(ctx context.Context, repo, workRoot, name, branch string, create bool, startPoint string) (string, error) {
	dir := filepath.Join(workRoot, name)
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		return "", fmt.Errorf("创建 worktree 根目录失败：%w", err)
	}
	removeWorktree(ctx, repo, dir)
	args := []string{"worktree", "add"}
	if create {
		args = append(args, "-b", branch, dir)
		if startPoint != "" {
			args = append(args, startPoint)
		}
	} else if startPoint != "" {
		args = append(args, "--detach", dir, startPoint)
	} else {
		args = append(args, dir, branch)
	}
	if _, err := gitOut(ctx, repo, args...); err != nil {
		return "", fmt.Errorf("创建 worktree %s 失败：%w", name, err)
	}
	return dir, nil
}

// removeWorktree 删除 worktree；失败时退回删目录并 prune，避免上次残留挡住下一次。
func removeWorktree(ctx context.Context, repo, dir string) {
	if dir == "" {
		return
	}
	if _, err := os.Stat(dir); err != nil {
		return
	}
	_, _ = gitOut(ctx, repo, "worktree", "remove", "--force", dir)
	_ = os.RemoveAll(dir)
	_, _ = gitOut(ctx, repo, "worktree", "prune")
}

func deleteBranch(ctx context.Context, repo, branch string) {
	if branch == "" {
		return
	}
	_, _ = gitOut(ctx, repo, "branch", "-D", branch)
}

// updateTarget 把基线分支合进目标分支（影子分支的每日同步）。
func updateTarget(ctx context.Context, targetDir, baseBranch string) error {
	if _, err := gitOut(ctx, targetDir, "merge", "--no-ff", "--no-edit", baseBranch); err != nil {
		_, _ = gitOut(ctx, targetDir, "merge", "--abort")
		return fmt.Errorf("目标分支同步 %s 失败：%w", baseBranch, err)
	}
	return nil
}

// commitAll 提交候选 worktree 的全部改动；没有改动时返回 errNoChanges。
func commitAll(ctx context.Context, dir, message string) (string, error) {
	// AGENTS.md 是引擎改完代码后自动写的项目约定，不是候选的真实改动。
	// 只留下它会让“什么都没改”的候选也看起来有 diff，必须剔掉。
	dropUntrackedAgents(ctx, dir)
	st, err := statusPorcelain(ctx, dir)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(st) == "" {
		return "", errNoChanges
	}
	if _, err := gitOut(ctx, dir, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := gitOut(ctx, dir, "-c", "user.name=WIN7Harness-RSI",
		"-c", "user.email=rsi@localhost", "commit", "-m", message); err != nil {
		return "", err
	}
	return gitOut(ctx, dir, "rev-parse", "HEAD")
}

var errNoChanges = fmt.Errorf("候选没有产生任何改动")

// dropUntrackedAgents 删除引擎自动生成、且尚未被 git 跟踪的 AGENTS.md。
// 如果项目本来就跟踪 AGENTS.md，则保留，交给正常 diff 流程。
func dropUntrackedAgents(ctx context.Context, dir string) {
	if _, err := gitOut(ctx, dir, "ls-files", "--error-unmatch", "AGENTS.md"); err == nil {
		return
	}
	_ = os.Remove(filepath.Join(dir, "AGENTS.md"))
}

// HasChanges 判断 worktree 里是否有真实改动（忽略自动生成的 AGENTS.md）。
// 实现器轮数到顶时，用它决定“把已有改动交给门禁”还是直接判失败。
func HasChanges(ctx context.Context, dir string) (bool, error) {
	dropUntrackedAgents(ctx, dir)
	st, err := statusPorcelain(ctx, dir)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(st) != "", nil
}

// diffStat 统计基线到候选的改动规模。
func diffStat(ctx context.Context, repo, baseBranch, candBranch string) (DiffStat, error) {
	out, err := gitOut(ctx, repo, "diff", "--numstat", baseBranch+"..."+candBranch)
	if err != nil {
		return DiffStat{}, err
	}
	var st DiffStat
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		parts := strings.Split(ln, "\t")
		if len(parts) < 3 {
			continue
		}
		st.Files++
		if parts[0] == "-" || parts[1] == "-" {
			continue
		}
		if n, err := strconv.Atoi(parts[0]); err == nil {
			st.Added += n
		}
		if n, err := strconv.Atoi(parts[1]); err == nil {
			st.Deleted += n
		}
	}
	return st, nil
}

// changedFiles 返回基线到候选的变更文件列表（相对仓库根）。
func changedFiles(ctx context.Context, repo, baseBranch, candBranch string) ([]string, error) {
	out, err := gitOut(ctx, repo, "diff", "--name-only", baseBranch+"..."+candBranch)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, ln := range strings.Split(out, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			files = append(files, filepath.ToSlash(ln))
		}
	}
	return files, nil
}

// mergeCandidate 把候选分支合入目标分支；冲突时自动 abort。
func mergeCandidate(ctx context.Context, targetDir, candBranch, message string) (string, error) {
	if _, err := gitOut(ctx, targetDir, "merge", "--no-ff", "--no-edit", "-m", message, candBranch); err != nil {
		_, _ = gitOut(ctx, targetDir, "merge", "--abort")
		return "", fmt.Errorf("合并 %s 失败：%w", candBranch, err)
	}
	return gitOut(ctx, targetDir, "rev-parse", "HEAD")
}

// revertLastMerge 回滚目标分支上最后一次合并。失败时保留现场，交给人看。
func revertLastMerge(ctx context.Context, targetDir string) (string, error) {
	if _, err := gitOut(ctx, targetDir, "revert", "-m", "1", "--no-edit", "HEAD"); err != nil {
		_, _ = gitOut(ctx, targetDir, "revert", "--abort")
		return "", fmt.Errorf("自动回滚失败：%w", err)
	}
	return gitOut(ctx, targetDir, "rev-parse", "HEAD")
}

func headHash(ctx context.Context, dir string) (string, error) {
	return gitOut(ctx, dir, "rev-parse", "HEAD")
}

// captureWorktreePatch 把未提交的改动（含新增文件）抓成 patch。
// 实现器失败时 worktree 会被清理，先把现场留下来，方便人工续做或复盘。
func captureWorktreePatch(ctx context.Context, dir string) string {
	if _, err := gitOut(ctx, dir, "add", "-A"); err != nil {
		return ""
	}
	p, err := gitOut(ctx, dir, "diff", "--cached")
	if err != nil {
		return ""
	}
	return p
}

package core

// Git 内建工作流：提交前预检、自动提交信息、基于 git 的安全回滚。
// 设计原则：git 不可用或目录不是仓库时，一律回落到 v1.22 的文件快照，
// 用户不需要理解两者差异，命令行为保持一致。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// nowStamp 返回用于标记的紧凑时间戳。
func nowStamp() string { return time.Now().Format("20060102-150405") }

// GitChange 一条工作区改动（来自 git status --short）。
type GitChange struct {
	Code string // A/M/D/R/U 等
	Path string
}

// Stage 返回改动的中文分类。
func (g GitChange) Stage() string {
	switch {
	case strings.Contains(g.Code, "U"), g.Code == "AA", g.Code == "DD":
		return "冲突"
	case strings.HasPrefix(g.Code, "A"), strings.HasPrefix(g.Code, "?"):
		return "新增"
	case strings.HasPrefix(g.Code, "D"):
		return "删除"
	case strings.HasPrefix(g.Code, "R"):
		return "重命名"
	default:
		return "修改"
	}
}

// IsGitRepo 判断 dir 是否为 git 工作区。
func IsGitRepo(dir string) bool {
	if strings.TrimSpace(dir) == "" || !GitAvailable() {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	// 兼容子目录工作区：.git 可能在上级
	out, err := GitRun(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// GitChanges 解析工作区改动清单。
func GitChanges(dir string) []GitChange {
	// core.quotepath=false：中文文件名不转成八进制转义（"\346\265\213"），
	// 否则界面显示乱码、大文件检测/StateStat 按路径查找都会落空。
	out, err := GitRun(dir, "-c", "core.quotepath=false", "status", "--short", "--untracked-files=all")
	if err != nil {
		return nil
	}
	var list []GitChange
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if strings.TrimSpace(ln) == "" || len(ln) < 3 {
			continue
		}
		code := strings.TrimSpace(ln[:2])
		p := strings.TrimSpace(ln[2:])
		// quotepath=false 后路径带引号也要能处理（含空格/特殊字符）
		p = strings.Trim(p, `"`)
		if i := strings.LastIndex(p, " -> "); i >= 0 {
			p = strings.TrimSpace(p[i+4:])
		}
		code = strings.ReplaceAll(code, " ", "")
		if code == "" {
			code = "M"
		}
		list = append(list, GitChange{Code: code, Path: p})
	}
	return list
}

// GitState 判断仓库是否处于多步操作中间态。
func GitState(dir string) string {
	gitDir := filepath.Join(dir, ".git")
	if st, err := os.Stat(gitDir); err != nil || !st.IsDir() {
		return ""
	}
	check := []struct{ path, name string }{
		{"rebase-merge", "rebase"},
		{"rebase-apply", "rebase"},
		{"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"},
		{"REVERT_HEAD", "revert"},
	}
	for _, c := range check {
		if _, err := os.Stat(filepath.Join(gitDir, c.path)); err == nil {
			return c.name
		}
	}
	return ""
}

// isGitIdentityErr 判断失败原因是不是"没配 user.name / user.email"。
// git 的原文有几种写法，不同版本的措辞不同，按关键片段匹配。
func isGitIdentityErr(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "author identity unknown") ||
		strings.Contains(s, "unable to auto-detect email") ||
		strings.Contains(s, "please tell me who you are") ||
		(strings.Contains(s, "empty ident name") && strings.Contains(s, "not allowed"))
}

// gitIdentity 返回本次提交该用的身份。
// 第三个返回值表示"用的是兜底身份"——只有这种情况才需要在界面上提示用户去配置。
func gitIdentity(dir string) (name, email string, isTemp bool) {
	name, _ = GitRun(dir, "config", "user.name")
	email, _ = GitRun(dir, "config", "user.email")
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	if name != "" && email != "" {
		return name, email, false
	}
	// 兜底身份固定不变：每次提交换一个名字会让历史看起来像多人协作。
	if name == "" {
		name = "WIN7Harness"
	}
	if email == "" {
		email = "win7harness@localhost"
	}
	return name, email, true
}

// GitPreflight 提交前预检。返回 block 非空表示不能自动提交，notes 为需要提醒的风险点。
// 预检只读，不改动仓库状态。
func GitPreflight(dir string) (block string, notes []string) {
	if !GitAvailable() {
		return "本机没有找到 git，无法执行 git 流程（回落到文件快照）", nil
	}
	if !IsGitRepo(dir) {
		return "当前工作目录不是 git 仓库：" + dir, nil
	}
	if st := GitState(dir); st != "" {
		return fmt.Sprintf("仓库正处在 %s 过程中，为避免把中间状态提交上去，已阻止自动提交。请先处理完（git %s --continue/--abort）再试。", st, st), nil
	}
	changes := GitChanges(dir)
	for _, c := range changes {
		if c.Stage() == "冲突" {
			return fmt.Sprintf("检测到未解决冲突：%s。请先解决冲突并 git add，再提交。", c.Path), nil
		}
	}
	if len(changes) == 0 {
		return "", []string{"工作区干净，没有需要提交的改动"}
	}
	// 风险提示：大批量删除与大文件
	var del int
	var big []string
	for _, c := range changes {
		if c.Stage() == "删除" {
			del++
		}
		if st, err := os.Stat(filepath.Join(dir, c.Path)); err == nil && st.Size() > 20<<20 {
			big = append(big, fmt.Sprintf("%s（%.1fMB）", c.Path, float64(st.Size())/(1<<20)))
		}
	}
	if del > 20 {
		notes = append(notes, fmt.Sprintf("本次包含 %d 个删除，提交前建议再确认一次", del))
	}
	if len(big) > 0 {
		if len(big) > 3 {
			big = big[:3]
		}
		notes = append(notes, "以下文件超过 20MB，提交会让仓库变大："+strings.Join(big, "、"))
	}
	// 第一次用 git 的机器几乎都没配身份。提前说清楚，别等提交失败才让人一脸茫然。
	if _, _, isTemp := gitIdentity(dir); isTemp {
		notes = append(notes, "本机未配置 git 身份（user.name / user.email），提交会自动使用临时身份 WIN7Harness <win7harness@localhost>")
	}
	return "", notes
}

// AutoCommitMessage 依据改动清单生成提交信息：按改动性质选前缀，正文列代表性目录。
// 纯函数，便于单测。
func AutoCommitMessage(changes []GitChange) string {
	if len(changes) == 0 {
		return "chore: 无改动"
	}
	var add, del, mod, doc, test int
	for _, c := range changes {
		switch c.Stage() {
		case "新增":
			add++
		case "删除":
			del++
		default:
			mod++
		}
		low := strings.ToLower(c.Path)
		if strings.HasSuffix(low, ".md") || strings.HasSuffix(low, ".txt") || strings.Contains(low, "/docs/") || strings.HasPrefix(low, "docs/") {
			doc++
		}
		if strings.HasSuffix(low, "_test.go") || strings.Contains(low, "/test/") || strings.HasPrefix(low, "test/") {
			test++
		}
	}
	n := len(changes)
	prefix := "chore"
	switch {
	case doc == n:
		prefix = "docs"
	case test == n:
		prefix = "test"
	case add > 0 && mod == 0 && del == 0:
		prefix = "feat"
	case mod > 0:
		prefix = "fix"
	}
	title := fmt.Sprintf("%s: 更新 %d 个文件", prefix, n)
	if add > 0 || del > 0 || mod > 0 {
		title += fmt.Sprintf("（新增 %d / 修改 %d / 删除 %d）", add, mod, del)
	}

	// 正文按目录归并，最多 6 行，避免提交信息比 diff 还长
	dirs := map[string]int{}
	for _, c := range changes {
		d := filepath.Dir(c.Path)
		d = strings.ReplaceAll(d, "\\", "/")
		if d == "." {
			d = "(根目录)"
		}
		dirs[d]++
	}
	keys := make([]string, 0, len(dirs))
	for k := range dirs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if dirs[keys[i]] != dirs[keys[j]] {
			return dirs[keys[i]] > dirs[keys[j]]
		}
		return keys[i] < keys[j]
	})
	var body []string
	for i, k := range keys {
		if i >= 6 {
			body = append(body, fmt.Sprintf("- 其余 %d 个目录", len(keys)-6))
			break
		}
		body = append(body, fmt.Sprintf("- %s：%d 个文件", k, dirs[k]))
	}
	return title + "\n\n" + strings.Join(body, "\n")
}

// CommitAll 预检 → add → commit。msg 为空时自动生成。
// 返回人读的结果文本；预检阻断类问题以 error 形式返回，调用方可直接展示。
func CommitAll(dir, msg string) (string, error) {
	block, notes := GitPreflight(dir)
	if block != "" {
		return "", fmt.Errorf("%s", block)
	}
	changes := GitChanges(dir)
	if len(changes) == 0 {
		return "工作区干净，没有需要提交的改动。", nil
	}
	msg = strings.TrimSpace(msg)
	auto := false
	if msg == "" {
		msg = AutoCommitMessage(changes)
		auto = true
	}
	if _, err := GitRun(dir, "add", "-A"); err != nil {
		return "", fmt.Errorf("git add 失败：%v", err)
	}
	out, err := GitRun(dir, "commit", "-m", msg)
	if err != nil && isGitIdentityErr(out) {
		// 没配过 git 身份的人（第一次用 git 的机器基本都是）不该被卡在这里。
		// 用临时身份把这次提交完成，并明确告诉用户"这只是兜底，建议配一次全局身份"。
		name, email, isTemp := gitIdentity(dir)
		if isTemp {
			notes = append(notes, fmt.Sprintf("本机未配置 git 身份，本次提交使用临时身份 %s <%s>；"+
				"要改成你自己的，执行：git config --global user.name \"你的名字\" 与 git config --global user.email \"你的邮箱\"", name, email))
			out2, err2 := GitRun(dir, "-c", "user.name="+name, "-c", "user.email="+email, "commit", "-m", msg)
			if err2 == nil {
				out, err = out2, nil
			} else {
				out = out2
			}
		}
	}
	if err != nil {
		detail := strings.TrimSpace(out)
		if strings.Contains(detail, "nothing to commit") {
			return "没有可提交的内容（可能是空改动）。", nil
		}
		return "", fmt.Errorf("git commit 失败：%v\n%s", err, detail)
	}
	head, _ := GitRun(dir, "log", "-1", "--oneline")
	var b strings.Builder
	b.WriteString("已提交：")
	b.WriteString(strings.TrimSpace(head))
	b.WriteString("\n\n提交信息（")
	if auto {
		b.WriteString("自动生成")
	} else {
		b.WriteString("手动填写")
	}
	b.WriteString("）：\n")
	b.WriteString(msg)
	if len(notes) > 0 {
		b.WriteString("\n\n提示：\n- ")
		b.WriteString(strings.Join(notes, "\n- "))
	}
	return b.String(), nil
}

// GitRewind 回滚最近 n 次提交带来的改动：先把当前未提交改动 stash 存档，
// 再 reset --hard 到 HEAD~n。存档不丢东西，用户可以 git stash pop 找回。
func GitRewind(dir string, n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("回滚步数必须大于 0")
	}
	if !IsGitRepo(dir) {
		return "", fmt.Errorf("当前工作目录不是 git 仓库")
	}
	if st := GitState(dir); st != "" {
		return "", fmt.Errorf("仓库正处在 %s 过程中，请先处理完再回滚", st)
	}
	out, err := GitRun(dir, "rev-list", "--count", "HEAD")
	if err != nil {
		return "", fmt.Errorf("读取提交数失败（仓库可能还没有提交）：%v", err)
	}
	var total int
	fmt.Sscanf(strings.TrimSpace(out), "%d", &total)
	if total < n {
		return "", fmt.Errorf("当前仓库只有 %d 个提交，无法回退 %d 步", total, n)
	}
	var b strings.Builder
	if len(GitChanges(dir)) > 0 {
		tag := "w7h-rewind-" + nowStamp()
		if _, err := GitRun(dir, "stash", "push", "-u", "-m", tag); err != nil {
			return "", fmt.Errorf("当前改动存档失败，已中止回滚（未改动任何文件）：%v", err)
		}
		b.WriteString("当前未提交改动已存档（可用 git stash pop 找回）：" + tag + "\n")
	}
	target := fmt.Sprintf("HEAD~%d", n)
	if out, err := GitRun(dir, "reset", "--hard", target); err != nil {
		return b.String(), fmt.Errorf("git reset 失败：%v\n%s", err, strings.TrimSpace(out))
	}
	head, _ := GitRun(dir, "log", "-1", "--oneline")
	b.WriteString(fmt.Sprintf("已回退 %d 步，当前 HEAD：%s", n, strings.TrimSpace(head)))
	return b.String(), nil
}

// GitIdentityFor 返回当前生效的 git 身份（没配时返回兜底值，isTemp=true）。
// 界面用它把输入框填成"现在到底是谁在提交"，避免用户以为配过了。
func GitIdentityFor(dir string) (name, email string, isTemp bool) {
	return gitIdentity(dir)
}

// SetGitIdentity 把身份写进 git 的全局配置。
// 只有用户明确点了"写入"才会走到这里——改的是全局配置，不该偷偷做。
func SetGitIdentity(name, email string) error {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	if name == "" || email == "" {
		return fmt.Errorf("名字与邮箱都不能为空")
	}
	if out, err := GitRun("", "config", "--global", "user.name", name); err != nil {
		return fmt.Errorf("写入 user.name 失败：%v\n%s", err, out)
	}
	if out, err := GitRun("", "config", "--global", "user.email", email); err != nil {
		return fmt.Errorf("写入 user.email 失败：%v\n%s", err, out)
	}
	return nil
}

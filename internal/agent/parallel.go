package agent

// 并行写子代理：多个会改文件的子任务各自在独立工作副本里干活，
// 全部结束后按文件粒度合并回主工作区；同一文件被两个子任务改过就算冲突，
// 保留先到的一份，另一份另存为 .conflict 文件交给用户裁决。

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"win7harness/internal/core"
	"win7harness/internal/llm"
	"win7harness/internal/tools"
)

// 副本里跳过的目录：这些内容与代码改动无关，复制它们只会拖慢速度。
var copySkipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "target": true, ".idea": true, ".vscode": true,
	"__pycache__": true, ".venv": true,
}

// copyMaxFile 单个文件超过该大小不复制（多为二进制产物）。
const copyMaxFile = 8 << 20

// isolatedRun 一个隔离副本的执行结果。
type isolatedRun struct {
	Desc    string
	Dir     string
	Text    string
	Changed []string // 相对路径
	Deleted []string
	Err     error
}

// copyTree 递归复制目录，跳过 .git 等大目录与超大文件。
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return nil
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		base := filepath.Base(path)
		if info.IsDir() {
			if copySkipDirs[base] {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !info.Mode().IsRegular() || info.Size() > copyMaxFile {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o755); err != nil {
			return err
		}
		return copyFile(path, filepath.Join(dst, rel))
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// snapshot 记录目录下每个文件的内容指纹。
func snapshot(dir string) map[string]string {
	m := map[string]string{}
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && copySkipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() > copyMaxFile {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return nil
		}
		if h, err := fileMD5(path); err == nil {
			m[filepath.ToSlash(rel)] = h
		}
		return nil
	})
	return m
}

func fileMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// diffSnapshot 对比前后快照，返回新增/修改与删除的相对路径。
func diffSnapshot(before, after map[string]string) (changed, deleted []string) {
	for p, h := range after {
		if old, ok := before[p]; !ok || old != h {
			changed = append(changed, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			deleted = append(deleted, p)
		}
	}
	sort.Strings(changed)
	sort.Strings(deleted)
	return
}

// MergeReport 合并结果汇总。
type MergeReport struct {
	Merged    []string
	Conflicts []string
	Deleted   []string
	Notes     []string
}

// String 渲染成给人看的文本。
func (r MergeReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "合并完成：写入 %d 个文件", len(r.Merged))
	if len(r.Conflicts) > 0 {
		fmt.Fprintf(&b, "，冲突 %d 个", len(r.Conflicts))
	}
	if len(r.Deleted) > 0 {
		fmt.Fprintf(&b, "，仅副本内删除 %d 个（未回写）", len(r.Deleted))
	}
	b.WriteString("\n")
	if len(r.Merged) > 0 {
		b.WriteString("\n已合入：\n")
		for _, p := range r.Merged {
			b.WriteString("  " + p + "\n")
		}
	}
	if len(r.Conflicts) > 0 {
		b.WriteString("\n冲突（同一文件被多个子任务改过，已保留第一个，其余另存）：\n")
		for _, c := range r.Conflicts {
			b.WriteString("  " + c + "\n")
		}
	}
	for _, n := range r.Notes {
		b.WriteString("\n" + n + "\n")
	}
	return b.String()
}

// MergeWrites 把各隔离副本的改动合并回 root（无基线指纹，仅副本间冲突检测）。
func MergeWrites(root string, runs []*isolatedRun) MergeReport {
	return MergeWritesWithBase(root, runs, nil)
}

// MergeWritesWithBase 同 MergeWrites，但 before 是任务开始前的工作区 MD5 快照
// （SpawnBatch 在复制出隔离副本前对主工作区取的）。
// 用它识别“用户在任务运行期间改了同名文件”：
//   - 有冲突：副本内容写成 <文件>.conflict-<描述>，主工作区原样保留，并进 Notes；
//   - 无冲突：目标覆盖前先 SnapshotFile——并行合并产生的改动也必须能 /undo。
func MergeWritesWithBase(root string, runs []*isolatedRun, before map[string]string) MergeReport {
	var rep MergeReport
	owner := map[string]string{}
	for _, r := range runs {
		if r == nil || r.Err != nil {
			continue
		}
		for _, rel := range r.Changed {
			if prev, ok := owner[rel]; ok {
				// 同一份内容（比如两个子任务都只改格式）不算冲突
				if sameContent(filepath.Join(root, rel), filepath.Join(r.Dir, rel)) {
					continue
				}
				dst := filepath.Join(root, rel+".from-"+sanitizeName(r.Desc))
				if err := copyFile(filepath.Join(r.Dir, rel), dst); err != nil {
					rep.Notes = append(rep.Notes, "冲突副本保存失败："+err.Error())
					continue
				}
				rep.Conflicts = append(rep.Conflicts, fmt.Sprintf("%s（%s 与 %s，后者另存为 %s）",
					rel, prev, r.Desc, filepath.Base(dst)))
				continue
			}
			src := filepath.Join(r.Dir, rel)
			dst := filepath.Join(root, rel)
			// 用户在批量任务运行期间改过同名文件？副本内容另存 .conflict，
			// 不覆盖用户的改动（此前是无条件覆盖）。
			if before != nil {
				if baseHash, ok := before[rel]; ok {
					if curHash, err := fileMD5(dst); err == nil && curHash != baseHash {
						dst2 := filepath.Join(root, rel+".conflict-"+sanitizeName(r.Desc))
						if err := copyFile(src, dst2); err != nil {
							rep.Notes = append(rep.Notes, "冲突副本保存失败："+err.Error())
							owner[rel] = r.Desc
							continue
						}
						rep.Conflicts = append(rep.Conflicts, fmt.Sprintf("%s（用户在任务运行期间修改，副本内容已另存为 %s）",
							rel, filepath.Base(dst2)))
						owner[rel] = r.Desc
						continue
					}
				}
			}
			// 覆盖前快照：并行合并的改动也要能 /undo 回滚。
			if _, err := os.Stat(dst); err == nil {
				_ = core.SnapshotFile(dst)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				rep.Notes = append(rep.Notes, "创建目录失败："+err.Error())
				continue
			}
			if err := copyFile(src, dst); err != nil {
				rep.Notes = append(rep.Notes, "写入失败 "+rel+"："+err.Error())
				continue
			}
			owner[rel] = r.Desc
			rep.Merged = append(rep.Merged, rel)
		}
		rep.Deleted = append(rep.Deleted, r.Deleted...)
	}
	sort.Strings(rep.Merged)
	return rep
}

func sameContent(a, b string) bool {
	ha, err1 := fileMD5(a)
	hb, err2 := fileMD5(b)
	return err1 == nil && err2 == nil && ha == hb
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "sub"
	}
	repl := func(r rune) rune {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|', ' ':
			return '_'
		}
		return r
	}
	return strings.Map(repl, s)
}

// SpawnBatch 并行执行多个写子任务（隔离副本 + 合并回写）。
// 这是 task 工具在"多个子任务都要改文件"时走的路径：不再退化成串行，
// 而是各写各的副本，最后一次性合并，边界清晰。
func (e *Engine) SpawnBatch(tasks []tools.BatchTask) (string, error) {
	if len(tasks) == 0 {
		return "", fmt.Errorf("没有子任务")
	}
	base := e.WorkDir
	before := snapshot(base)

	root, err := os.MkdirTemp("", "w7h-parallel-")
	if err != nil {
		return "", fmt.Errorf("创建隔离目录失败：%v", err)
	}
	defer os.RemoveAll(root)

	runs := make([]*isolatedRun, len(tasks))
	var wg sync.WaitGroup
	// 子任务并发上限读内存档位（2G→1，4G→2，标准 3）：
	// 此前容量=任务数且在主 goroutine 里 acquire，形同虚设，
	// 2G 机器上一次派发 8 个写子任务 = 8 份工作区副本 + 8 个模型会话直接打爆。
	subLimit := 1
	if p := core.MemProfile(); p.SubTasks > 0 {
		subLimit = p.SubTasks
	}
	sem := make(chan struct{}, subLimit)
	for i, t := range tasks {
		wg.Add(1)
		i, t := i, t // 每次迭代独立：外层闭包按引用捕获，必须局部化
		core.SafeGo("agent/parallel.go:269", func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			func(i int, t tools.BatchTask) {
				defer wg.Done()
				r := &isolatedRun{Desc: t.Desc}
				dir := filepath.Join(root, fmt.Sprintf("w%d", i))
				if err := copyTree(base, dir); err != nil {
					r.Err = fmt.Errorf("复制工作区失败：%v", err)
					runs[i] = r
					return
				}
				r.Dir = dir
				e.emit(Event{Kind: "status", Text: "并行子任务·" + t.Desc + "：执行中（隔离副本）"})

				subModel := e.ModelFor(JobSubWrite)
				sub := New(e.Client.WithModel(subModel), subModel, dir)
				sub.Temp = e.Temp
				sub.MaxTok = e.MaxTok
				sub.UserInstructions = e.UserInstructions
				sub.Confirm = e.Confirm
				sub.ConfirmDiff = e.ConfirmDiff
				sub.PlanMode = e.PlanMode
				sub.SystemExtra = subResultFormat
				sub.depth = e.depth + 1
				// 与 spawnSubWithDef 对齐：并行写子代理同等遵守权限策略——
				// 此前从不设 Policy，deny/ask 规则与路径判定全部失效。
				sub.Policy = e.Policy.Clone()
				sub.ToolProfile = e.ToolProfile
				sub.maxRound = DefaultSubRounds()
				var content strings.Builder
				var lastErr string
				sub.Emit = func(ev Event) {
					switch ev.Kind {
					case "content":
						content.WriteString(ev.Text)
					case "error":
						lastErr = ev.Text
					case "status":
						if ev.Text != "" && ev.Text != "思考中…" {
							e.emit(Event{Kind: "status", Text: "并行·" + t.Desc + "：" + ev.Text})
						}
					}
				}
				sub.mu.Lock()
				sub.Messages = append(sub.Messages, llm.Message{Role: "user", Content: t.Prompt})
				sub.mu.Unlock()

				ctx, cancel := context.WithTimeout(e.parentCtx(), e.subTimeout())
				defer cancel()
				sub.loop(ctx)
				r.Text = content.String()
				if lastErr != "" {
					r.Err = fmt.Errorf("%s", lastErr)
				}
				in, outTok, turns := sub.Usage()
				fi, fo, _ := sub.fastUsage()
				e.absorbSubUsage(subModel, llm.Usage{PromptTokens: in + fi, CompletionTokens: outTok + fo}, fi, fo, turns)
				r.Changed, r.Deleted = diffSnapshot(before, snapshot(dir))
				runs[i] = r
			}(i, t)
		})
	}
	wg.Wait()

	rep := MergeWritesWithBase(base, runs, before)
	var b strings.Builder
	b.WriteString(rep.String())
	b.WriteString("\n各子任务结论：\n")
	for i, r := range runs {
		if r == nil {
			continue
		}
		fmt.Fprintf(&b, "\n【%d/%d %s】\n", i+1, len(tasks), tasks[i].Desc)
		if r.Err != nil {
			fmt.Fprintf(&b, "失败：%v\n", r.Err)
			continue
		}
		text := strings.TrimSpace(r.Text)
		if text == "" {
			text = "（完成，但没有返回文本）"
		}
		b.WriteString(clipSubResult(text, e.subBudget()) + "\n")
		if len(r.Changed) > 0 {
			fmt.Fprintf(&b, "改动文件：%s\n", strings.Join(r.Changed, ", "))
		}
	}
	return strings.TrimSpace(b.String()), nil
}

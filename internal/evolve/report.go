package evolve

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SaveRun 把一轮报告写成 run.json / run.md。
func SaveRun(outDir string, rep *RunReport) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "run.json"), b, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "run.md"), []byte(rep.Markdown()), 0o644)
}

// WritePatch 保存候选的完整 diff，清理 worktree 后仍可复查。
func WritePatch(outDir, id, patch string) error {
	if strings.TrimSpace(patch) == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(outDir, id+".patch"), []byte(patch), 0o644)
}

// AppendHistory 追加一条实验记录到 rsi-out/history.jsonl。
func AppendHistory(repo string, exp Experiment) error {
	dir := filepath.Join(repo, "rsi-out")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "history.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(exp)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// LoadHistory 读取最近 limit 条实验记录；坏行跳过，不让历史文件拖垮整轮。
func LoadHistory(repo string, limit int) []Experiment {
	if limit <= 0 {
		return nil
	}
	f, err := os.Open(filepath.Join(repo, "rsi-out", "history.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var all []Experiment
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" {
			continue
		}
		var e Experiment
		if err := json.Unmarshal([]byte(ln), &e); err != nil {
			continue
		}
		all = append(all, e)
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all
}

// Markdown 生成人类可读报告。
func (r *RunReport) Markdown() string {
	var b strings.Builder
	b.WriteString("# WIN7 Harness 自迭代报告\n\n")
	fmt.Fprintf(&b, "- 运行：`%s`\n- 时间：%s → %s\n", r.ID, r.StartedAt, r.EndedAt)
	fmt.Fprintf(&b, "- 仓库：`%s`\n- 分支：`%s` → `%s`\n", r.Repo, r.BaseBranch, r.TargetBranch)
	fmt.Fprintf(&b, "- 模式：%s\n- 停止原因：%s", modeText(r.DryRun), r.Stop)
	if r.StopDetail != "" {
		b.WriteString("（" + r.StopDetail + "）")
	}
	b.WriteString("\n")
	if len(r.BaseEval.Results) > 0 {
		fmt.Fprintf(&b, "- 基线评测：通过 %d / 失败 %d / 跳过 %d，通过率 %.1f%%\n",
			r.BaseEval.Pass, r.BaseEval.Fail, r.BaseEval.Skip, r.BaseEval.Score()*100)
	}
	fmt.Fprintf(&b, "- 消耗：%d token，耗时 %.1f 分钟\n\n", r.Tokens, float64(r.DurationMs)/60000)

	if len(r.Proposals) > 0 {
		b.WriteString("## 提案\n\n")
		for i, p := range r.Proposals {
			fmt.Fprintf(&b, "%d. [%s] **%s** — %s\n", i+1, p.Risk, p.Title, p.Change)
			if p.Acceptance != "" {
				b.WriteString("   - 验收：" + p.Acceptance + "\n")
			}
		}
		b.WriteString("\n")
	}

	if len(r.Experiments) == 0 {
		b.WriteString("## 候选\n\n本轮没有执行候选。\n")
	} else {
		b.WriteString("## 候选\n\n")
		b.WriteString("| 候选 | 提案 | 风险 | 状态 | 改动 | 门禁 | 耗时 |\n")
		b.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")
		for _, e := range r.Experiments {
			gate := "-"
			if e.Gate != nil {
				if e.Gate.OK {
					gate = "通过"
				} else {
					gate = "失败"
				}
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %d 文件 +%d/-%d | %s | %.1fs |\n",
				e.ID, clip(e.Title, 60), e.Risk, e.Status, e.Diff.Files, e.Diff.Added, e.Diff.Deleted,
				gate, float64(e.DurationMs)/1000)
		}
		b.WriteString("\n")
		for _, e := range r.Experiments {
			if e.Reason == "" && e.Gate == nil {
				continue
			}
			fmt.Fprintf(&b, "### %s %s\n\n", e.ID, e.Title)
			if e.Reason != "" {
				b.WriteString("- 结论：" + e.Reason + "\n")
			}
			if e.Commit != "" {
				b.WriteString("- 候选提交：`" + shortHash(e.Commit) + "`\n")
			}
			if e.MergedCommit != "" {
				b.WriteString("- 合并提交：`" + shortHash(e.MergedCommit) + "`\n")
			}
			if e.Gate != nil {
				b.WriteString("\n")
				for _, st := range e.Gate.Steps {
					mark := "✅"
					if !st.OK {
						mark = "❌"
					}
					fmt.Fprintf(&b, "- %s %s（%dms）%s\n", mark, st.Name, st.Ms, firstLine(st.Detail))
				}
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n> Stage 0 为影子阶段：只合并目标分支，不改基线分支；合并后门禁失败会自动回滚。\n")
	return b.String()
}

func modeText(dry bool) string {
	if dry {
		return "dry-run（只提案，不执行）"
	}
	return "shadow（合并到影子分支）"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return " — " + clip(s, 160)
}

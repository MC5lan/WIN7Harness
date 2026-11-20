//go:build windows

package app

import (
	"os"
	"path/filepath"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/eval"
)

// 诊断页的两个附加页签：评测基线、崩溃报告。
//
// 这两样以前都是"打一段文字给你看"：评测只告诉你通过率，看不到哪条挂了；
// 崩溃只列一串路径，还得自己去文件夹里翻。做成页签后能一条条看、能直接打开。

// ---------- 评测基线（保留仅为被调用） ----------

func (a *App) runEvalAsync() {
	// 必须取地址：a.diag 是值类型，复制一份的话 goroutine 里写的结果全丢，
	// 界面会永远停在"还没跑过"（踩过）。
	d := &a.diag
	d.mu.Lock()
	if d.evalBusy {
		d.mu.Unlock()
		return
	}
	d.evalBusy = true
	d.mu.Unlock()
	a.Redraw()

	core.SafeGo("app/diag_extra.go:37", func() {
		root := filepath.Join(core.LocalDataDir(), "eval", "ui-tmp")
		_ = os.RemoveAll(root)
		rep := eval.Run(root, false)
		// 报告落盘：命令行与界面看的是同一份文件，排查时不会各说各话。
		outDir := filepath.Join(core.LocalDataDir(), "eval")
		if err := os.MkdirAll(outDir, 0o755); err == nil {
			_ = core.WriteFileAtomic(filepath.Join(outDir, "eval-report.md"), []byte(rep.Markdown()), 0o644)
		}
		d.mu.Lock()
		d.evalRep = &rep
		d.evalAt = time.Now()
		d.evalBusy = false
		d.mu.Unlock()
		a.Redraw()
	})
}

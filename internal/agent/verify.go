package agent

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/tools"
)

// 验证闭环（auto-verify）。
//
// 竞品普遍在做这件事的原因很实在：模型"改完了"和"改对了"是两件事。
// 只靠模型自查，最常见的结果是它宣布完成，而代码根本编译不过。
// 这里的做法是把"构建 + 测试"变成模型回合的一部分：
//
//	本轮发生过写盘/执行类工具 → 模型准备收尾时自动跑一次验证
//	→ 失败就把精简后的失败报告回灌成一条新消息，让模型在同一轮里继续修
//	→ 默认最多 3 次，避免和环境问题死磕烧 token。
//
// 开关：VerifyMode = "off" | "auto"，未设置时看环境变量 W7H_VERIFY=1。

// DefaultMaxVerifyRounds 自动验证的默认最大轮数。
const DefaultMaxVerifyRounds = 3

// VerifyModeOff / VerifyModeAuto 是合法的模式值。
const (
	VerifyModeOff  = "off"
	VerifyModeAuto = "auto"
)

// verifyMu / wroteThisTurn 记录"本轮是否动过代码"。
// 工具批处理会并行执行只读工具，写类工具串行，两个路径都会调 markWrite，
// 因此必须用独立互斥量而不是 e.mu（execToolRaw 内部已有 e.mu 的临界区）。
var _ = sync.Mutex{}

// verifyEnabled 判断是否应对本引擎启用自动验证。
func (e *Engine) verifyEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(e.VerifyMode)) {
	case VerifyModeOff:
		return false
	case VerifyModeAuto:
		return true
	}
	return strings.TrimSpace(os.Getenv("W7H_VERIFY")) == "1"
}

// VerifyEnabled 供界面查询当前是否启用自动验证。
func (e *Engine) VerifyEnabled() bool { return e.verifyEnabled() }

// VerifyLimit 返回自动验证的最大轮数（含默认值）。
func (e *Engine) VerifyLimit() int {
	if e.MaxVerifyRounds > 0 {
		return e.MaxVerifyRounds
	}
	return DefaultMaxVerifyRounds
}

// CurrentRound 返回当前请求内正在执行的工具循环轮次（1 起），供恢复日志记录进度。
func (e *Engine) CurrentRound() int {
	e.verifyMu.Lock()
	defer e.verifyMu.Unlock()
	return e.curRound
}

// MarkWrite 记录本轮发生过写类操作，供自动验证判断"有没有必要跑"。
// paths 可选：能定位到具体文件时，自动验证只跑受影响包。
func (e *Engine) MarkWrite(name string, paths ...string) {
	if name == "" || tools.IsReadOnly(name) {
		return
	}
	e.verifyMu.Lock()
	defer e.verifyMu.Unlock()
	e.wroteThisTurn = true
	if e.changedPaths == nil {
		e.changedPaths = map[string]bool{}
	}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p != "" {
			e.changedPaths[p] = true
		}
	}
	// 写类工具但没有可定位的路径（run_command/gofmt/apply_patch 解析失败）：
	// 无法判断受影响包，自动验证退回全量。
	if len(paths) == 0 {
		e.writeUntracked = true
	}
}

// changedPathsNow 返回本轮能定位的写入路径；不可定位时返回 nil，表示全量验证。
func (e *Engine) changedPathsNow() []string {
	e.verifyMu.Lock()
	defer e.verifyMu.Unlock()
	if e.writeUntracked || len(e.changedPaths) == 0 {
		return nil
	}
	out := make([]string, 0, len(e.changedPaths))
	for p := range e.changedPaths {
		out = append(out, p)
	}
	return out
}

// resetWriteFlag 在每次用户发新消息时清空标记。
func (e *Engine) resetWriteFlag() {
	e.verifyMu.Lock()
	e.wroteThisTurn = false
	e.changedPaths = nil
	e.writeUntracked = false
	e.verifyMu.Unlock()
}

// wroteThisTurnNow 返回本轮是否动过写类操作。
func (e *Engine) wroteThisTurnNow() bool {
	e.verifyMu.Lock()
	defer e.verifyMu.Unlock()
	return e.wroteThisTurn
}

// verifyBudget 返回单次验证的步超时。
func (e *Engine) verifyBudget() time.Duration {
	if e.VerifyTimeout > 0 {
		return e.VerifyTimeout
	}
	return 5 * time.Minute
}

// runVerify 执行一次构建 + 测试验证。
func (e *Engine) runVerify(ctx context.Context) tools.VerifyResult {
	tctx := &tools.Context{
		WorkDir: e.WorkDir,
		Ctx:     ctx, // 用户点“停止”时杀掉构建/测试的整棵进程树，而不是空转到完
		Progress: func(s string) {
			e.emit(Event{Kind: "status", Text: s})
		},
	}
	if e.Confirm != nil {
		tctx.Confirm = e.Confirm
	}
	// 用户点了停止时不要继续跑构建，否则"停止"要等几分钟才生效。
	// 能定位到改动文件时只跑受影响包；run_command 等不可定位的改动退回全量。
	done := make(chan tools.VerifyResult, 1)
	targets := e.changedPathsNow()
	core.SafeGo("agent/verify.go:113", func() {
		done <- tools.VerifyProjectTargets(tctx, e.WorkDir, "", e.verifyBudget(), targets)
	})
	select {
	case res := <-done:
		return res
	case <-ctx.Done():
		return tools.VerifyResult{Skipped: "用户已停止"}
	}
}

// autoVerify 在模型准备收尾时决定是否插入一轮验证。
// 返回值 report 非空表示需要把失败报告回灌给模型继续。
func (e *Engine) autoVerify(ctx context.Context, tries *int) (report string, ran bool) {
	if !e.verifyEnabled() || !e.wroteThisTurnNow() {
		return "", false
	}
	limit := e.MaxVerifyRounds
	if limit <= 0 {
		limit = DefaultMaxVerifyRounds
	}
	if *tries >= limit {
		e.emit(Event{Kind: "notice", Text: "自动验证已达上限（" +
			itoa(limit) + " 次），仍未全绿。请查看上面的失败摘要后自行决定是否继续。"})
		return "", false
	}
	*tries++
	if len(e.changedPathsNow()) > 0 {
		e.emit(Event{Kind: "status", Text: "自动验证中（构建 + 受影响包测试）…"})
	} else {
		e.emit(Event{Kind: "status", Text: "自动验证中（构建 + 全量测试）…"})
	}
	res := e.runVerify(ctx)
	if res.Skipped != "" {
		e.emit(Event{Kind: "notice", Text: res.VerifySummary()})
		return "", false
	}
	e.emit(Event{Kind: "notice", Text: res.VerifySummary()})
	if res.OK {
		return "", true
	}
	return res.Report + "\n\n" + tools.HumanVerifyHint, true
}

// itoa 避免为一个整数格式化引入 strconv 到热点路径（可读性优先，量级极小）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

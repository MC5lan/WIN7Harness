package tools

import (
	"fmt"
	"strings"
	"time"

	"win7harness/internal/core"
)

func init() {
	register(Tool{
		Name: "run_command",
		Desc: "在本地 shell 中执行命令（Windows 下为 cmd.exe，可执行 go build、git、npm、python 等）。输出会按 UTF-8/GBK 自动解码；列目录/找文件优先 list_dir/glob，不要用 dir /s 或 where /r 全盘扫。默认等待命令结束并返回退出码与输出；background=true 时立即返回任务 ID，长跑命令（dev server、watch、日志）必须用它，之后用 task_output 读输出、task_kill 终止。危险命令执行前会请求用户确认。",
		Schema: `{
  "type":"object",
  "properties":{
    "command":{"type":"string","description":"要执行的完整命令行"},
    "cwd":{"type":"string","description":"工作目录，默认当前工作目录"},
    "timeout":{"type":"integer","description":"超时秒数，默认 120，最大 1800（background 模式下表示后台任务最长寿命）"},
    "background":{"type":"boolean","description":"是否放后台执行。会立即返回任务 ID，适合 dev server、长编译、tail 日志等不会自己结束的命令。"}
  },
  "required":["command"]
}`,
		Run: runCommand,
	})
}

// maxOutputBytes 单次命令回给模型的输出上限。
//
// 256KB 大约 6~10 万 token，一条 `go test ./...` 就能把上下文吃光，
// 随后触发压缩、丢失任务状态——看起来就是“越做越乱”。
// 48KB 足够容纳编译错误与失败用例；需要全文时重定向到文件后分段 read_file，
// 或用 background=true + task_output 增量读。
const maxOutputBytes = 48 * 1024

func runCommand(ctx *Context, args map[string]interface{}) (Result, error) {
	cmdline := strings.TrimSpace(Get(args, "command"))
	if cmdline == "" {
		return Result{}, fmt.Errorf("命令为空")
	}
	if err := guardBroadCommand(cmdline); err != nil {
		return Result{}, err
	}
	cwd := core.ResolvePath(ctx.WorkDir, Get(args, "cwd"))
	timeout := GetInt(args, "timeout", 120)
	_, hasTimeout := args["timeout"]
	if timeout <= 0 {
		timeout = 120
	}
	if timeout > 1800 {
		timeout = 1800
	}

	background := false
	if v, ok := args["background"].(bool); ok {
		background = v
	}

	// 风险分级：中/高危命令必须经用户确认。
	// 确认回调缺失（子代理/无人值守/批处理）时 fail-closed——此前是静默放行，
	// 等于让无人值守的自动化顺手执行 rd /s（见 tools 审计 TL-P1-2）。
	risk := core.CommandRisk(cmdline)
	if risk >= core.RiskCaution && !ctx.PreApproved {
		level := "中风险"
		if risk == core.RiskDanger {
			level = "高风险"
		}
		if ctx.Confirm == nil {
			return Result{}, fmt.Errorf("命令属于%s，当前没有确认通道（子代理/无人值守），已拒绝执行：%s", level, cmdline)
		}
		if !ctx.Confirm("执行命令（"+level+"）", "命令：\n"+cmdline+"\n\n目录："+cwd) {
			return Result{}, fmt.Errorf("用户拒绝执行该命令")
		}
	}

	if background {
		// 前台 timeout 的默认值 120s 不能直接当后台寿命：不传 timeout 的
		// dev server/watch 应该活 20 分钟，而不是 2 分钟。只有显式传了
		// timeout 才按用户的秒数限制。
		ttl := bgDefaultTTL
		if hasTimeout && timeout > 0 {
			ttl = time.Duration(timeout) * time.Second
		}
		return startBackgroundCommand(ctx, cmdline, cwd, ttl)
	}

	if ctx.Progress != nil {
		ctx.Progress("$ " + cmdline)
	}

	// 必须把 ctx 的取消信号传给 runShell：用户点“停止”时要杀整棵进程树，
	// 否则鼠标/键盘自动化脚本会继续在后台动，直到 timeout 才结束。
	out, code, err := runShell(cmdline, cwd, time.Duration(timeout)*time.Second, nil, ctxDone(ctx))
	if err != nil && out == "" {
		return Result{}, err
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("退出码: %d\n", code))
	if strings.TrimSpace(out) == "" {
		b.WriteString("（无输出）")
	} else {
		b.WriteString(out)
	}
	return Result{Output: b.String()}, err
}

// limitedBuffer 有容量上限的缓冲：超出后继续计数但丢弃内容，保证 Write 永不返回短写。
type limitedBuffer struct {
	buf       strings.Builder
	limit     int
	truncated bool
	total     int
	mu        chan struct{}
}

func newLimited(limit int) *limitedBuffer {
	lb := &limitedBuffer{limit: limit, mu: make(chan struct{}, 1)}
	lb.mu <- struct{}{}
	return lb
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	<-l.mu
	defer func() { l.mu <- struct{}{} }()
	l.total += len(p)
	remain := l.limit - l.buf.Len()
	if remain > 0 {
		if len(p) <= remain {
			l.buf.Write(p)
		} else {
			l.buf.Write(p[:remain])
			l.truncated = true
		}
	} else {
		l.truncated = true
	}
	return len(p), nil // 必须返回 len(p)，否则 io.Copy 会报 ErrShortWrite
}

func (l *limitedBuffer) String() string {
	<-l.mu
	defer func() { l.mu <- struct{}{} }()
	s := l.buf.String()
	if l.truncated {
		s += fmt.Sprintf("\n\n...（输出超过 %s 已截断，共 %s）", core.HumanSize(int64(l.limit)), core.HumanSize(int64(l.total)))
	}
	return s
}

// Package mcp 实现一个极简的 MCP（Model Context Protocol）stdio 客户端。
// 只依赖标准库与 Go 1.20 之前的语法，保证 Win7 32/64 位可用。
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"win7harness/internal/core"
)

// protocolVersion 使用 MCP 2024-11-05 版协议（stdio 传输）。
// 注意：与 internal/mcp/remote.go 的 remote 协议刻意不同——
// stdio 侧用 2024-11-05（主流 npx/uvx MCP 服务器都支持，最稳），
// remote Streamable HTTP 侧用 2025-03-26（其 MCP-Protocol-Version 头是规范强制的）。
// 两个版本不是矛盾，分别对应两种传输各自的最佳兼容档位。
const protocolVersion = "2024-11-05"

// ToolInfo 一个 MCP 工具的描述。
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("MCP 错误码 %d", e.Code)
	}
	return fmt.Sprintf("MCP 错误 %d：%s", e.Code, e.Message)
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Client 一个已启动的 MCP 服务连接。
type Client struct {
	Name string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	stderr *ringBuffer

	mu     sync.Mutex
	seq    int64
	closed bool

	// job 与 jobOK：子进程挂到 KILL_ON_JOB_CLOSE 的 Job Object（Windows）。
	// 父进程崩溃/退出时 Windows 自动杀整棵进程树，避免孤儿 MCP 常驻。
	job   uintptr
	jobOK bool

	// remote 非 nil 表示这是远程 MCP（HTTP/SSE），没有子进程。
	remote *remoteConn
}

// ringBuffer 保留最近若干字节的 stderr，便于报错时回显。
type ringBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newRing(max int) *ringBuffer { return &ringBuffer{max: max} }

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.max {
		r.buf = r.buf[len(r.buf)-r.max:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

// Start 建立 MCP 连接并完成初始化握手。
// 配了 url 走远程 HTTP/SSE，否则按 command 起本地子进程。
func Start(spec core.MCPServer) (*Client, error) {
	if strings.TrimSpace(spec.URL) != "" {
		return startRemote(spec)
	}
	if strings.TrimSpace(spec.Command) == "" {
		return nil, fmt.Errorf("服务 %s 既没有 command 也没有 url，无法连接", spec.Name)
	}
	cmd, err := newMCPCommand(spec.Command, spec.Args)
	if err != nil {
		return nil, err
	}
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Env = append(cmd.Env, "PYTHONIOENCODING=utf-8")
	hideMCPWindow(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("创建 stdin 管道失败：%s", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("创建 stdout 管道失败：%s", err)
	}
	er := newRing(16 * 1024)
	cmd.Stderr = er

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, fmt.Errorf("启动失败（%s）：%s", spec.Command, err)
	}
	// 挂到 KILL_ON_JOB_CLOSE 的 Job：父进程退出时 Windows 自动杀整棵树。
	// 绑不上（Win7 不支持嵌套 Job）则走 taskkill /T 兜底，kill 时按 PID 收割。
	job, jobOK := assignJob(cmd.Process.Pid)

	c := &Client{
		Name:   spec.Name,
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReaderSize(stdout, 64*1024),
		stderr: er,
		job:    job,
		jobOK:  jobOK,
	}

	if err := c.initialize(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// startRemote 建立远程 MCP 连接（HTTP/SSE）。
func startRemote(spec core.MCPServer) (*Client, error) {
	rc, err := newRemote(spec)
	if err != nil {
		return nil, err
	}
	c := &Client{Name: spec.Name, remote: rc}
	if err := c.initialize(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) initialize() error {
	params := map[string]interface{}{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "WIN7Harness",
			"version": core.Version,
		},
	}
	var res struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := c.call("initialize", params, &res, 30*time.Second); err != nil {
		return fmt.Errorf("初始化失败：%s（%s）", err, c.stderrTail())
	}
	// 按协议补一条 initialized 通知，失败不影响使用
	_ = c.notify("notifications/initialized", map[string]interface{}{})
	return nil
}

// ListTools 拉取工具清单。
func (c *Client) ListTools() ([]ToolInfo, error) {
	var res struct {
		Tools []ToolInfo `json:"tools"`
	}
	if err := c.call("tools/list", map[string]interface{}{}, &res, 30*time.Second); err != nil {
		return nil, err
	}
	return res.Tools, nil
}

// CallTool 调用一个工具并返回文本结果。
func (c *Client) CallTool(name string, args map[string]interface{}) (string, error) {
	if args == nil {
		args = map[string]interface{}{}
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	params := map[string]interface{}{"name": name, "arguments": args}
	if err := c.call("tools/call", params, &res, 120*time.Second); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, item := range res.Content {
		if item.Type == "text" || item.Type == "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(item.Text)
		} else {
			fmt.Fprintf(&b, "\n[%s 内容已省略]", item.Type)
		}
	}
	out := b.String()
	if res.IsError {
		return "", fmt.Errorf("%s", strings.TrimSpace(out))
	}
	return out, nil
}

// notify 发送通知（无 id，不等待响应）。
func (c *Client) notify(method string, params interface{}) error {
	if c.remote != nil {
		return c.remote.notify(method, params)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("连接已关闭")
	}
	msg := map[string]interface{}{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	line, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := c.stdin.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

// call 发送请求并读取匹配 id 的响应。请求串行化，读到不匹配的通知帧直接跳过。
func (c *Client) call(method string, params interface{}, out interface{}, timeout time.Duration) error {
	if c.remote != nil {
		return c.remote.call(method, params, out, timeout)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("连接已关闭")
	}
	c.seq++
	id := c.seq

	msg := map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	line, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := c.stdin.Write(append(line, '\n')); err != nil {
		c.closed = true
		return fmt.Errorf("写入失败：%s", err)
	}

	type readResult struct {
		line []byte
		err  error
	}
	ch := make(chan readResult, 1)
	core.SafeGo("mcp/client.go:275", func() {
		for {
			l, err := c.reader.ReadBytes('\n')
			if err != nil {
				ch <- readResult{nil, err}
				return
			}
			l = []byte(strings.TrimSpace(string(l)))
			if len(l) == 0 {
				continue
			}
			var probe struct {
				ID     *int64          `json:"id"`
				Method string          `json:"method"`
				Result json.RawMessage `json:"result"`
				Error  *rpcError       `json:"error"`
			}
			if err := json.Unmarshal(l, &probe); err != nil {
				continue // 非 JSON 行（日志等）直接忽略
			}
			if probe.ID == nil {
				continue // 服务端主动通知
			}
			if *probe.ID != id {
				continue // 历史残留帧
			}
			ch <- readResult{l, nil}
			return
		}
	})

	select {
	case r := <-ch:
		if r.err != nil {
			c.closed = true
			return fmt.Errorf("读取响应失败：%s%s", r.err, c.stderrTail())
		}
		var resp rpcResponse
		if err := json.Unmarshal(r.line, &resp); err != nil {
			return fmt.Errorf("解析响应失败：%s", err)
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("解析结果失败：%s", err)
			}
		}
		return nil
	case <-time.After(timeout):
		c.closed = true
		c.killTree()
		_ = c.reap(3 * time.Second)
		return fmt.Errorf("%s 超时（%s）%s", method, timeout, c.stderrTail())
	}
}

func (c *Client) stderrTail() string {
	if c.remote != nil {
		if tail := strings.TrimSpace(c.remote.tail()); tail != "" {
			return "\n最近一次错误：" + tail
		}
		return ""
	}
	s := strings.TrimSpace(c.stderr.String())
	if s == "" {
		return ""
	}
	if len(s) > 600 {
		s = s[len(s)-600:]
	}
	return "\n服务端 stderr：" + s
}

// killTree 收割整棵进程树：先 taskkill /T（按 PID 树枚举），
// 再 TerminateJobObject 收口（Job 真绑上时），最后 Process.Kill 兜底。
func (c *Client) killTree() {
	if c.cmd == nil || c.cmd.Process == nil {
		return
	}
	killProcessTreePS(c.cmd.Process.Pid)
	if c.jobOK && c.job != 0 {
		terminateJob(c.job)
	}
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

// reap 等待子进程收尸（含超时保护），并关闭 Job 句柄。
// 此前 Close/timeout 两条路径都漏了 Wait——超时 kill 的进程
// 句柄与 stderr 读取 goroutine 永远泄漏，长跑低配机句柄数缓慢上涨。
func (c *Client) reap(timeout time.Duration) error {
	if c.cmd == nil {
		return nil
	}
	done := make(chan struct{})
	core.SafeGo("mcp/client.go:reap", func() {
		_ = c.cmd.Wait()
		close(done)
	})
	select {
	case <-done:
	case <-time.After(timeout):
	}
	if c.job != 0 {
		closeHandle(c.job)
		c.job = 0
	}
	return nil
}

// Close 关闭连接并结束子进程。
func (c *Client) Close() error {
	c.mu.Lock()
	already := c.closed
	c.closed = true
	c.mu.Unlock()

	if c.remote != nil {
		return c.remote.close()
	}
	if already {
		// 之前超时/异常路径标记过 closed：只补收尸，不能直接 return。
		return c.reap(3 * time.Second)
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	// 先等 3 秒自行退出（多数 MCP 收到 stdin 关闭后会自己退），
	// 3 秒没退就收整棵树，最后统一收尸并关 Job。
	done := make(chan struct{})
	core.SafeGo("mcp/client.go:close", func() { _ = c.cmd.Wait(); close(done) })
	select {
	case <-done:
		if c.job != 0 {
			closeHandle(c.job)
			c.job = 0
		}
		return nil
	case <-time.After(3 * time.Second):
		c.killTree()
		return c.reap(3 * time.Second)
	}
}

// Alive 连接是否仍可用。
func (c *Client) Alive() bool {
	if c.remote != nil {
		return c.remote.alive()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	return c.cmd != nil && c.cmd.Process != nil
}

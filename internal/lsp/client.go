// Package lsp 是一个最小但可用的 Language Server Protocol 客户端。
//
// 目标不是实现完整 LSP，而是让 Agent 能拿到 IDE 级别的：
//   - 编译/类型诊断（textDocument/publishDiagnostics）
//   - 定义跳转（textDocument/definition）
//   - 引用查找（textDocument/references）
//   - 悬停信息（textDocument/hover）
//
// 传输是 LSP 标准的 stdio + Content-Length 帧。默认自动启动
// gopls / pylsp / typescript-language-server / rust-analyzer。
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Position 是 LSP 的 0-based 行列。
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range 是 LSP 的文本区间。
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Diagnostic 是 LSP 诊断。
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source"`
}

// Location 是定义/引用位置。
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type Client struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader

	mu     sync.Mutex
	nextID int
	pend   map[int]chan rpcResponse

	diagMu   sync.Mutex
	diags    map[string][]Diagnostic
	diagSeen map[string]bool

	done chan struct{}
}

// Start 启动语言服务器并完成 initialize 握手。
func Start(ctx context.Context, command string, args []string, root string) (*Client, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = CanonicalPath(root)
	hideWindow(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if os.Getenv("WIN7H_LSP_DEBUG") == "1" {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = io.Discard
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{
		cmd: cmd, in: in, out: bufio.NewReaderSize(out, 64*1024),
		pend: map[int]chan rpcResponse{}, diags: map[string][]Diagnostic{},
		diagSeen: map[string]bool{}, done: make(chan struct{}),
	}
	go c.readLoop()
	initCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	params := map[string]interface{}{
		"processId": os.Getpid(),
		"rootUri":   PathToURI(root),
		"capabilities": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"publishDiagnostics": map[string]interface{}{"relatedInformation": true},
				"definition":         map[string]interface{}{"linkSupport": false},
				"references":         map[string]interface{}{},
				"hover":              map[string]interface{}{"contentFormat": []string{"plaintext", "markdown"}},
			},
		},
	}
	if err := c.request(initCtx, "initialize", params, nil); err != nil {
		c.Close()
		return nil, fmt.Errorf("LSP initialize 失败：%w", err)
	}
	_ = c.notify("initialized", map[string]interface{}{})
	return c, nil
}

func hideWindow(cmd *exec.Cmd) {
	// 平台差异放在各自文件里，避免这里 import syscall 影响非 Windows 测试。
	hideWindowPlatform(cmd)
}

func (c *Client) readLoop() {
	defer close(c.done)
	for {
		body, err := c.readFrame()
		if err != nil {
			return
		}
		var msg struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			rpcResponse
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		if os.Getenv("WIN7H_LSP_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "[lsp] method=%s id=%v params=%s\n", msg.Method, msg.ID, string(msg.Params))
		}
		if msg.Method == "textDocument/publishDiagnostics" {
			c.handleDiagnostics(msg.Params)
			continue
		}
		if msg.ID != nil {
			c.mu.Lock()
			ch := c.pend[*msg.ID]
			delete(c.pend, *msg.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- msg.rpcResponse
			}
		}
	}
}

func (c *Client) readFrame() ([]byte, error) {
	const maxFrameBytes = 16 << 20
	const maxHeaderLine = 8 << 10
	length := -1
	for {
		line, err := c.out.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if len(line) > maxHeaderLine {
			return nil, fmt.Errorf("LSP 头部行过长（%d 字节），已中止", len(line))
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			n, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.ToLower(line), "content-length:")))
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("缺少 Content-Length")
	}
	if length > maxFrameBytes {
		return nil, fmt.Errorf("LSP 帧过大（%d 字节，上限 %d），已中止", length, maxFrameBytes)
	}
	buf := make([]byte, length)
	_, err := io.ReadFull(c.out, buf)
	return buf, err
}

func (c *Client) write(msg interface{}) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.in.Write(body)
	return err
}

func (c *Client) notify(method string, params interface{}) error {
	return c.write(map[string]interface{}{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *Client) request(ctx context.Context, method string, params interface{}, result interface{}) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResponse, 1)
	c.pend[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pend, id)
		c.mu.Unlock()
	}()
	if err := c.write(map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return fmt.Errorf("语言服务器已退出")
	case resp := <-ch:
		if resp.Error != nil {
			return fmt.Errorf("%s", resp.Error.Message)
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
}

func (c *Client) handleDiagnostics(raw json.RawMessage) {
	var p struct {
		URI         string       `json:"uri"`
		Diagnostics []Diagnostic `json:"diagnostics"`
	}
	if json.Unmarshal(raw, &p) != nil || p.URI == "" {
		return
	}
	key := diagKey(p.URI)
	c.diagMu.Lock()
	c.diags[key] = p.Diagnostics
	c.diagSeen[key] = true
	c.diagMu.Unlock()
}

// diagKey 统一 URI 的编码差异：gopls 可能回 file:///C:/x，也可能回
// file:///C%3A/x；按本地路径比较，避免 WaitDiagnostics 永远等不到。
func diagKey(uri string) string {
	p := URIToPath(uri)
	if p == "" {
		return uri
	}
	return strings.ToLower(filepath.Clean(p))
}

// DidOpen 把文件内容同步给语言服务器。
func (c *Client) DidOpen(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	uri := PathToURI(path)
	lang := languageID(path)
	err = c.notify("textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri, "languageId": lang, "version": 1, "text": string(data),
		},
	})
	return uri, err
}

// WaitDiagnostics 等 publishDiagnostics 到达（或超时），返回该 URI 的诊断。
func (c *Client) WaitDiagnostics(ctx context.Context, uri string, timeout time.Duration) []Diagnostic {
	deadline := time.Now().Add(timeout)
	key := diagKey(uri)
	for {
		c.diagMu.Lock()
		seen := c.diagSeen[key]
		d := append([]Diagnostic(nil), c.diags[key]...)
		c.diagMu.Unlock()
		if seen || time.Now().After(deadline) {
			return d
		}
		select {
		case <-ctx.Done():
			return d
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (c *Client) Definition(ctx context.Context, uri string, line, col int) ([]Location, error) {
	var raw json.RawMessage
	if err := c.request(ctx, "textDocument/definition", posParams(uri, line, col), &raw); err != nil {
		return nil, err
	}
	return decodeLocations(raw), nil
}

func (c *Client) References(ctx context.Context, uri string, line, col int) ([]Location, error) {
	params := posParams(uri, line, col)
	params["context"] = map[string]interface{}{"includeDeclaration": true}
	var raw json.RawMessage
	if err := c.request(ctx, "textDocument/references", params, &raw); err != nil {
		return nil, err
	}
	return decodeLocations(raw), nil
}

func (c *Client) Hover(ctx context.Context, uri string, line, col int) (string, error) {
	var raw json.RawMessage
	if err := c.request(ctx, "textDocument/hover", posParams(uri, line, col), &raw); err != nil {
		return "", err
	}
	var h struct {
		Contents interface{} `json:"contents"`
	}
	if json.Unmarshal(raw, &h) != nil {
		return "", nil
	}
	return renderHover(h.Contents), nil
}

func posParams(uri string, line, col int) map[string]interface{} {
	return map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"position":     map[string]interface{}{"line": line, "character": col},
	}
}

func decodeLocations(raw json.RawMessage) []Location {
	var locs []Location
	if json.Unmarshal(raw, &locs) == nil {
		return locs
	}
	var one Location
	if json.Unmarshal(raw, &one) == nil && one.URI != "" {
		return []Location{one}
	}
	return nil
}

func renderHover(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]interface{}:
		if s, ok := x["value"].(string); ok {
			return s
		}
	case []interface{}:
		var parts []string
		for _, item := range x {
			if s := renderHover(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// Close 结束语言服务器及其子进程。
//
// 这是一次性调用（每次工具调用新起一个 server），不需要保留索引状态；
// 先直接杀进程树，避免 gopls 退出后它拉起的 go list 变成孤儿占住临时目录。
func (c *Client) Close() {
	// 先杀树再关 stdin：反过来 gopls 会先退出，它拉起的 go list 会变成孤儿。
	killTreePlatform(c.cmd)
	_ = c.in.Close()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		<-c.done
	}
	// Wait 释放进程句柄/管道；不调用它，Windows 上临时目录会被语言服务器占住。
	_ = c.cmd.Wait()
}

func languageID(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".js":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".rs":
		return "rust"
	}
	return "plaintext"
}

// CanonicalPath 把 Windows 8.3 短路径（如 （MC5L~1）还原成真实长路径。
// gopls 会严格校验路径大小写/长名，短路径会导致 workspace folder 加载失败。
func CanonicalPath(path string) string {
	if v, err := filepath.EvalSymlinks(path); err == nil && v != "" {
		return v
	}
	return path
}

// PathToURI 把本地绝对路径转成 LSP file URI。
func PathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	abs = CanonicalPath(abs)
	u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(abs)}
	return u.String()
}

// URIToPath 把 LSP file URI 转回本地路径。
func URIToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	p := u.Path
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:] // Windows: /C:/x -> C:/x
	}
	return filepath.FromSlash(p)
}

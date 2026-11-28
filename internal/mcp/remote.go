package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"win7harness/internal/core"
)

// 远程 MCP：走 Streamable HTTP（POST JSON-RPC，响应可能是 application/json，
// 也可能是 text/event-stream 的 SSE 帧）。stdio 版是长连接读行，
// 这里每请求一次 HTTP，靠 Mcp-Session-Id 维持会话状态。
type remoteConn struct {
	name    string
	url     string
	headers []string
	client  *http.Client

	mu      sync.Mutex
	seq     int64
	session string
	lastErr string
	closed  bool
}

func newRemote(spec core.MCPServer) (*remoteConn, error) {
	u := strings.TrimSpace(spec.URL)
	if u == "" {
		return nil, fmt.Errorf("服务 %s 未配置 url", spec.Name)
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return nil, fmt.Errorf("服务 %s 的 url 必须以 http:// 或 https:// 开头", spec.Name)
	}
	// 明文 http 远程 MCP + 非私网地址：Bearer 明文出网，必须让用户知道
	if strings.HasPrefix(u, "http://") {
		if hpu, err := url.Parse(u); err == nil {
			if h := strings.ToLower(hpu.Hostname()); !isMCPLocalHost(h) {
				core.Logf("mcp: 远程 MCP %q 使用明文 http 且不在私网内：Authorization 会被明文发出去（%s）", spec.Name, h)
			}
		}
	}
	return &remoteConn{
		name:    spec.Name,
		url:     u,
		headers: spec.Headers,
		client: &http.Client{
			// 不在 Client 上设固定超时：每次调用按自己的 timeout 走 context，
			// 此前 tools/call 想要 120s 却被 90s 截断。
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// 不允许 https → http 降级：降级后 Authorization 会被明文发出去。
				for i := len(via) - 1; i >= 0; i-- {
					if via[i].URL.Scheme == "https" && req.URL.Scheme == "http" {
						return fmt.Errorf("拒绝 https → http 的重定向（凭据会明文发送）")
					}
				}
				if len(via) >= 10 {
					return fmt.Errorf("重定向过多（>10）")
				}
				return nil
			},
		},
	}, nil
}

// isMCPLocalHost 判断是否明显是本机/私网地址。
func isMCPLocalHost(h string) bool {
	h = strings.ToLower(strings.Trim(h, "[]"))
	if h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return true
	}
	for _, p := range []string{"10.", "192.168.", "169.254.", "fd", "fc", "fe80"} {
		if strings.HasPrefix(h, p) {
			return true
		}
	}
	if strings.HasPrefix(h, "172.") {
		parts := strings.Split(h, ".")
		if len(parts) >= 2 {
			var n int
			if _, err := fmt.Sscanf(parts[1], "%d", &n); err == nil && n >= 16 && n <= 31 {
				return true
			}
		}
	}
	return false
}

func (r *remoteConn) call(method string, params interface{}, out interface{}, timeout time.Duration) error {
	return r.callContext(context.Background(), method, params, out, timeout)
}

func (r *remoteConn) callContext(ctx context.Context, method string, params interface{}, out interface{}, timeout time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("连接已关闭")
	}
	r.seq++
	return r.roundTripContext(ctx, method, params, r.seq, out, timeout)
}

// notify 发通知（无 id，不需要响应）。SSE 服务可能仍回 202/204，忽略正文即可。
func (r *remoteConn) notify(method string, params interface{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	msg := map[string]interface{}{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	_, err := r.post(msg, 0, 20*time.Second, nil)
	return err
}

func (r *remoteConn) roundTrip(method string, params interface{}, id int64, out interface{}, timeout time.Duration) error {
	return r.roundTripContext(context.Background(), method, params, id, out, timeout)
}

func (r *remoteConn) roundTripContext(ctx context.Context, method string, params interface{}, id int64, out interface{}, timeout time.Duration) error {
	msg := map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	_, err := r.postContext(ctx, msg, id, timeout, out)
	return err
}

// post 发一条 JSON-RPC 消息；响应为 SSE 时逐帧找 id 匹配的那条。
func (r *remoteConn) post(msg map[string]interface{}, wantID int64, timeout time.Duration, out interface{}) (json.RawMessage, error) {
	return r.postContext(context.Background(), msg, wantID, timeout, out)
}

func (r *remoteConn) postContext(base context.Context, msg map[string]interface{}, wantID int64, timeout time.Duration, out interface{}) (json.RawMessage, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", r.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	// 2025-03-26 是 Streamable HTTP 规范引入的协议版本；
	// 它要求请求头带 MCP-Protocol-Version，这是 2024-11-05 没有的。
	// 故 remote 传输刻意用新版（stdio 见 client.go，用 2024-11-05 兼容主流服务器）。
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	if r.session != "" {
		req.Header.Set("Mcp-Session-Id", r.session)
	}
	for _, h := range r.headers {
		k, v, ok := strings.Cut(h, ":")
		if ok {
			req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}

	// 单次请求的超时由 http.Client 兜底，这里再收一层 context 便于快速失败。
	resp, err := r.client.Do(req)
	if err != nil {
		r.lastErr = err.Error()
		return nil, fmt.Errorf("请求 %s 失败：%s", r.name, core.ExplainNetErr(err))
	}
	defer resp.Body.Close()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		r.session = sid
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		txt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%s 鉴权失败（%d）：请检查 headers 里的 Authorization。%s", r.name, resp.StatusCode, strings.TrimSpace(string(txt)))
	}
	if resp.StatusCode >= 400 {
		txt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		r.lastErr = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return nil, fmt.Errorf("%s 返回 HTTP %d：%s", r.name, resp.StatusCode, strings.TrimSpace(string(txt)))
	}
	if wantID == 0 {
		return nil, nil
	}

	ctype := resp.Header.Get("Content-Type")
	if strings.Contains(ctype, "text/event-stream") {
		return r.readSSE(bufio.NewReader(io.LimitReader(resp.Body, 8<<20)), wantID, out, timeout)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败：%s", err)
	}
	return r.decode(raw, wantID, out)
}

func (r *remoteConn) readSSE(br *bufio.Reader, wantID int64, out interface{}, timeout time.Duration) (json.RawMessage, error) {
	deadline := time.Now().Add(timeout)
	const maxEventBytes = 1 << 20 // 单个 SSE 事件上限 1MB，防对端不给 \n 一路喂爆内存
	var data bytes.Buffer
	var frag bytes.Buffer
	flushEvent := func() (json.RawMessage, bool, error) {
		if data.Len() == 0 {
			return nil, false, nil
		}
		raw := append([]byte(nil), data.Bytes()...)
		data.Reset()
		done, derr := r.decodeMaybe(raw, wantID, out)
		return raw, done, derr
	}
	readCapped := func() (string, error) {
		frag.Reset()
		for {
			part, err := br.ReadSlice('\n')
			frag.Write(part)
			if frag.Len() > maxEventBytes {
				return "", fmt.Errorf("SSE 单行（含多帧累积）超过 %d 字节上限，已中止", maxEventBytes)
			}
			if err == bufio.ErrBufferFull {
				continue
			}
			return frag.String(), err
		}
	}
	for time.Now().Before(deadline) {
		line, err := readCapped()
		if line != "" {
			t := strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(t, "data:"):
				if data.Len() > maxEventBytes {
					return nil, fmt.Errorf("SSE 事件超过 %d 字节上限，已中止", maxEventBytes)
				}
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(t, "data:")))
			case t == "":
				if raw, done, derr := flushEvent(); derr != nil {
					return nil, derr
				} else if done {
					return raw, nil
				}
			}
		}
		if err != nil {
			if raw, done, derr := flushEvent(); derr != nil {
				return nil, derr
			} else if done {
				return raw, nil
			}
			if err == io.EOF {
				return nil, fmt.Errorf("%s 的 SSE 流提前结束，未收到 id=%d 的响应", r.name, wantID)
			}
			return nil, fmt.Errorf("读取 SSE 失败：%s", core.ExplainNetErr(err))
		}
	}
	return nil, fmt.Errorf("%s 响应超时（等待 id=%d）", r.name, wantID)
}

// decode 解析非流式响应体。
func (r *remoteConn) decode(raw []byte, wantID int64, out interface{}) (json.RawMessage, error) {
	done, err := r.decodeMaybe(raw, wantID, out)
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, fmt.Errorf("%s 返回的响应里没有 id=%d 的结果", r.name, wantID)
	}
	return raw, nil
}

// decodeMaybe 判断一条帧是不是我们要的那条响应；不是（通知/别的 id）就跳过。
func (r *remoteConn) decodeMaybe(raw []byte, wantID int64, out interface{}) (bool, error) {
	var resp rpcResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		if len(bytes.TrimSpace(raw)) == 0 {
			return false, nil
		}
		return false, fmt.Errorf("%s 返回了无法解析的内容：%s", r.name, firstLine(string(raw)))
	}
	if resp.ID == 0 && resp.Error == nil && len(resp.Result) == 0 {
		return false, nil // 通知帧
	}
	if resp.ID != wantID {
		return false, nil
	}
	if resp.Error != nil {
		return true, resp.Error
	}
	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return true, fmt.Errorf("解析 %s 的结果失败：%s", r.name, err)
		}
	}
	return true, nil
}

func (r *remoteConn) close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

func (r *remoteConn) alive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.closed
}

func (r *remoteConn) tail() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastErr
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i > 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

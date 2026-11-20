package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"win7harness/internal/core"
)

// DoctorStep 是一次探测的结果。
type DoctorStep struct {
	Name    string
	Status  int
	Body    string
	Latency time.Duration
	Err     error
}

func (s DoctorStep) ok() bool { return s.Err == nil && s.Status > 0 && s.Status < 400 }

// DoctorReport 是 /doctor 的结构化结果。
type DoctorReport struct {
	At            time.Time
	BaseURL       string
	Endpoint      string
	Model         string
	UserAgent     string
	SessionHeader bool
	Models        DoctorStep
	Chat          DoctorStep
	Tools         DoctorStep
}

// OK 表示基础对话可用；工具调用失败只算警告。
func (r DoctorReport) OK() bool { return r.Chat.ok() }

func (r DoctorReport) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "端点诊断 %s\r\n", r.At.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "地址：%s\r\n", r.Endpoint)
	fmt.Fprintf(&b, "Base：%s\r\n", r.BaseURL)
	fmt.Fprintf(&b, "模型：%s\r\n", emptyDash(r.Model))
	fmt.Fprintf(&b, "User-Agent：%s\r\n", r.UserAgent)
	if r.SessionHeader {
		fmt.Fprintf(&b, "x-opencode-session：已发送\r\n")
	}
	b.WriteString("\r\n")
	writeStep(&b, "1. GET /models", r.Models)
	writeStep(&b, "2. 最小对话（无工具）", r.Chat)
	writeStep(&b, "3. 最小对话（带工具）", r.Tools)
	b.WriteString("\r\n")
	switch {
	case r.Chat.ok():
		b.WriteString("结论：基础对话可用。")
		if !r.Tools.ok() && r.Tools.Status != 0 {
			b.WriteString(" 工具调用失败，建议换支持 tools 的模型，或只在纯聊天里使用。")
		}
	case r.Chat.Status == 0:
		b.WriteString("结论：对话请求没有发出去，检查网络/代理/TLS。")
	default:
		b.WriteString("结论：基础对话失败，先按上面的上游 message 修端点/Key/模型；这通常不是程序 bug。")
	}
	return b.String()
}

func writeStep(b *strings.Builder, name string, s DoctorStep) {
	if s.Status == 0 && s.Err != nil {
		fmt.Fprintf(b, "%s：失败 %v\r\n", name, s.Err)
		return
	}
	if s.Status == 0 {
		fmt.Fprintf(b, "%s：跳过（%s）\r\n", name, emptyDash(s.Body))
		return
	}
	fmt.Fprintf(b, "%s：HTTP %d（%dms）\r\n", name, s.Status, s.Latency.Milliseconds())
	if s.Body != "" {
		for _, ln := range strings.Split(s.Body, "\n") {
			fmt.Fprintf(b, "    %s\r\n", ln)
		}
	}
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "（空）"
	}
	return s
}

// Doctor 依次探测 /models、无工具对话、带工具对话。
// 只发极短请求，不生成完整回答；不会打印 API Key。
func (c *Client) Doctor(ctx context.Context) DoctorReport {
	rep := DoctorReport{
		At:            time.Now(),
		BaseURL:       c.BaseURL,
		Endpoint:      c.Endpoint(),
		Model:         c.Model,
		UserAgent:     "WIN7Harness/" + core.Version,
		SessionHeader: core.IsOpenCodeGo(c.BaseURL),
	}
	rep.Models = c.probeModels(ctx)
	rep.Chat = c.probeChat(ctx, false)
	if rep.Chat.ok() {
		rep.Tools = c.probeChat(ctx, true)
	} else {
		rep.Tools = DoctorStep{Name: "tools", Body: "基础对话未通过，跳过工具探测"}
	}
	return rep
}

// CheckChat 供“测试连接”使用：发一条非流式最短对话，验证 Key/模型/请求体。
func (c *Client) CheckChat(ctx context.Context) error {
	s := c.probeChat(ctx, false)
	if s.Err != nil {
		return s.Err
	}
	if s.Status >= 400 {
		if s.Body != "" {
			return fmt.Errorf("HTTP %d：%s", s.Status, s.Body)
		}
		return fmt.Errorf("HTTP %d", s.Status)
	}
	return nil
}

func (c *Client) probeModels(ctx context.Context) DoctorStep {
	step := DoctorStep{Name: "models"}
	base := strings.TrimSuffix(c.Endpoint(), "/chat/completions")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		step.Err = err
		return step
	}
	c.setHeaders(req)
	return c.doProbe(req, step)
}

func (c *Client) probeChat(ctx context.Context, withTools bool) DoctorStep {
	step := DoctorStep{Name: "chat"}
	body := map[string]interface{}{
		"model":      c.Model,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 16,
		"stream":     false,
	}
	if withTools {
		step.Name = "tools"
		body["tools"] = []map[string]interface{}{{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "ping",
				"description": "Return ok.",
				"parameters": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		}}
		body["tool_choice"] = "auto"
	}
	raw, err := json.Marshal(body)
	if err != nil {
		step.Err = err
		return step
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint(), bytes.NewReader(raw))
	if err != nil {
		step.Err = err
		return step
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	c.setHeaders(req)
	return c.doProbe(req, step)
}

func (c *Client) doProbe(req *http.Request, step DoctorStep) DoctorStep {
	start := time.Now()
	resp, err := c.http.Do(req)
	step.Latency = time.Since(start)
	if err != nil {
		step.Err = err
		return step
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	step.Status = resp.StatusCode
	step.Body = snippet(string(data), 900)
	return step
}

var skRe = regexp.MustCompile(`sk-[A-Za-z0-9_-]{6,}`)

func snippet(s string, max int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = skRe.ReplaceAllString(s, "sk-***")
	// 上游错误偶尔会回显 Authorization；保守打码。
	if i := strings.Index(strings.ToLower(s), "bearer "); i >= 0 {
		end := i + len("bearer ")
		for end < len(s) && s[end] != ' ' && s[end] != '"' && s[end] != '\n' {
			end++
		}
		if end > i+len("bearer ") {
			s = s[:i] + "Bearer ***" + s[end:]
		}
	}
	if len([]rune(s)) > max {
		s = string([]rune(s)[:max]) + "…"
	}
	return s
}

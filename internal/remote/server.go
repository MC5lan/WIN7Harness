package remote

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"win7harness/internal/core"
)

// nonceKey 把本次请求的 CSP nonce 传给页面 handler。
type nonceKey struct{}

func newCSPNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// —— 局域网指挥台 ——
//
// 目标：人在外面用手机浏览器看一眼"它还在跑吗"，顺手补一句"顺便把测试也跑一遍"。
//
// 三条硬边界，写死在实现里而不是写在文档里：
//  1. 默认只监听局域网地址，不碰公网、不做任何穿透。要远程用就自己配端口映射，
//     那是用户自己的网络决定，产品不替他开这个口子。
//  2. 配对码 6 位、5 分钟过期、同 IP 连续错 5 次锁 60 秒；配对后发的是随机 token，
//     与服务端会话绑定，无 token 一律 401。
//  3. 手机只能"发一句话"与"看状态"，不能直接调工具、改配置、点确认。
//     高危操作仍然回到电脑上确认——远程能做的事越少，出事的面积越小。
//
// 传输用普通 HTTP + 长轮询，不用 WebSocket：老环境下少一层协议就少一类兼容问题，
// 手机浏览器也不用装任何东西。

// Command 手机投过来的一条指令。
type Command struct {
	ID   string    `json:"id"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Event 推给手机的事件（AI 回复片段、状态变化）。
type Event struct {
	Seq  int64     `json:"seq"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Status 电脑端当前状态快照。
type Status struct {
	Version string `json:"version"`
	Model   string `json:"model"`
	WorkDir string `json:"work_dir"`
	Busy    bool   `json:"busy"`
	Pending int    `json:"pending"`
	Note    string `json:"note,omitempty"`
}

const (
	PairCodeTTL    = 5 * time.Minute
	TokenTTL       = 7 * 24 * time.Hour
	MaxPairFails   = 5
	PairLockWindow = 60 * time.Second
	MaxEvents      = 200
	MaxCommands    = 100
	MaxBodyBytes   = 64 << 10
)

// Server 局域网指挥台。
type Server struct {
	mu        sync.Mutex
	code      string
	codeUntil time.Time
	tokens    map[string]time.Time
	cmds      []Command
	events    []Event
	seq       int64
	fails     map[string]int
	locked    map[string]time.Time
	statusFn  func() Status
	submitFn  func(Command)
	cmdSeq    int
	srv       *http.Server
	ln        net.Listener
	addr      string
	https     bool

	// v1.28：审批回传与会话查看
	approvals  map[string]*approvalReq
	approvalOn bool
	sessDir    string

	// v1.29：停止 / 计划 / 设备 / 指标（实现见 v129.go）
	stopFn       func() (string, bool)
	plansFn      func() []PlanView
	planSwitch   func(id string, on bool) (bool, string)
	pollInFlight int32
	devicesFn    func(callerHash string) []DeviceView
	revokeFn     func(key string) (string, bool)
	deviceVerify func(tok string) bool
	deviceAdd    func(name, tok string)
	metricsFn    func() interface{}
}

// NewServer 建一个未启动的服务。statusFn/submitFn 由 app 注入：
// 服务本身不碰引擎，方便单独测试与复用。
func NewServer() *Server {
	return &Server{
		tokens:    map[string]time.Time{},
		fails:     map[string]int{},
		locked:    map[string]time.Time{},
		approvals: map[string]*approvalReq{},
		statusFn:  func() Status { return Status{} },
	}
}

// SetSessionDir 指定会话目录，手机端据此查看历史会话。
func (s *Server) SetSessionDir(dir string) {
	s.mu.Lock()
	s.sessDir = dir
	s.mu.Unlock()
}

// SessionDir 当前会话目录。
func (s *Server) SessionDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessDir
}

// SetHTTPS 设置下次启动时是否使用自签 TLS。
// 只影响下一次 Start/StartHTTPS，运行中的监听不会被静默切协议——
// 半路换协议会让已配对的手机直接断连，还得重新扫码。
func (s *Server) SetHTTPS(on bool) {
	s.mu.Lock()
	s.https = on
	s.mu.Unlock()
}

// HTTPS 当前是否走 HTTPS。
func (s *Server) HTTPS() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.https
}

// BaseURL 手机可访问的根地址（未启动时为空）。
// 用 schema 而不是写死 http，是因为手机端页面要据此拼自己的接口地址。
func (s *Server) BaseURL() string {
	s.mu.Lock()
	addr := s.addr
	https := s.https
	s.mu.Unlock()
	if addr == "" {
		return ""
	}
	schema := "http"
	if https {
		schema = "https"
	}
	return schema + "://" + displayHost(addr) + "/"
}

// PairURL 配对用的二维码内容：地址带上配对码，
// 手机扫完直接进页面并自动配对，省掉在手机上敲 6 位数这一步。
func (s *Server) PairURL() string {
	base := s.BaseURL()
	if base == "" {
		return ""
	}
	return base + "#p=" + s.PairCode()
}

// SetStatus 注入状态提供者。
func (s *Server) SetStatus(fn func() Status) {
	s.mu.Lock()
	s.statusFn = fn
	s.mu.Unlock()
}

// SetSubmit 注入指令提交者。
func (s *Server) SetSubmit(fn func(Command)) {
	s.mu.Lock()
	s.submitFn = fn
	s.mu.Unlock()
}

// PairCode 返回当前配对码，过期则重新生成。
func (s *Server) PairCode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.code == "" || now.After(s.codeUntil) {
		s.code = randomDigits(6)
		s.codeUntil = now.Add(PairCodeTTL)
	}
	return s.code
}

func randomDigits(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		v := time.Now().UnixNano()
		for i := range buf {
			buf[i] = byte('0' + v%10)
			v /= 10
		}
		return string(buf)
	}
	for i := range buf {
		buf[i] = byte('0' + int(buf[i])%10)
	}
	return string(buf)
}

func randomToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// Addr 已监听地址（未启动时为空）。
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// ListenAddr 拼监听地址：lan=true 监听所有网卡（局域网可访问），否则仅本机。
func ListenAddr(lan bool, port int) string {
	if port <= 0 {
		port = 8765
	}
	if lan {
		return ":" + strconv.Itoa(port)
	}
	return "127.0.0.1:" + strconv.Itoa(port)
}

// Start 启动 HTTP 监听。返回可直接贴到手机浏览器的地址。
func (s *Server) Start(addr string) (string, error) {
	return s.start(addr, nil, nil)
}

// StartHTTPS 启动 HTTPS 监听（自签证书）。
//
// 为什么默认不开：手机第一次访问自签站点必然弹一次"不安全"的警告，
// 用户得点"继续访问"才能进。局域网里防的是同屋蹭网的人，
// 而这个风险用一次性配对码 + token 已经挡掉大半，所以 HTTPS 定位是"可选加固"。
func (s *Server) StartHTTPS(addr string, certPEM, keyPEM []byte) (string, error) {
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		return "", errors.New("HTTPS 需要证书与私钥")
	}
	return s.start(addr, certPEM, keyPEM)
}

func (s *Server) start(addr string, certPEM, keyPEM []byte) (string, error) {
	s.mu.Lock()
	if s.srv != nil {
		s.mu.Unlock()
		return s.BaseURL(), nil
	}
	s.mu.Unlock()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("监听 %s 失败：%v", addr, err)
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      40 * time.Second, // 长轮询要留够时间
		// 慢连接占着 fd 不释放是 LAN 上的常见资源耗尽方式
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	useTLS := len(certPEM) > 0
	serveLn := ln
	if useTLS {
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			_ = ln.Close()
			return "", fmt.Errorf("证书无效：%v", err)
		}
		cfg := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12, // Go 的 TLS 栈是纯 Go 实现，任何平台都支持 1.2；老手机（Android 4.4+ 全部支持）
		}
		serveLn = tls.NewListener(ln, cfg)
	}
	s.mu.Lock()
	s.ln = ln
	s.srv = srv
	s.addr = ln.Addr().String()
	s.https = useTLS
	s.mu.Unlock()
	core.SafeGo("remote/server.go:296", func() { _ = srv.Serve(serveLn) })
	return s.BaseURL(), nil
}

// displayHost 把 0.0.0.0:8765 换成第一个可用的局域网 IP，方便直接播报给用户。
func displayHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		for _, ip := range LocalIPs() {
			return net.JoinHostPort(ip, port)
		}
		return net.JoinHostPort("127.0.0.1", port)
	}
	return addr
}

// Stop 关闭服务。
func (s *Server) Stop() error {
	s.mu.Lock()
	srv := s.srv
	ln := s.ln
	s.srv = nil
	s.ln = nil
	s.addr = ""
	s.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
	if ln != nil {
		_ = ln.Close()
	}
	return nil
}

// Running 是否在监听。
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.srv != nil
}

// LocalIPs 列出本机可用的局域网 IPv4 地址（不含回环）。
func LocalIPs() []string {
	// 虚拟网卡（VirtualBox / VMware / Hyper-V / WSL）的地址常常排在真实网卡前面，
	// 而那个地址手机是连不上的。这里把两类分开收集，真实网卡优先；
	// 只有虚拟网卡时（程序就跑在虚拟机里）再退回虚拟网卡，不会变成空列表。
	var real, virt []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return real
	}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		dst := &real
		if virtualIface(ifi.Name) {
			dst = &virt
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				*dst = append(*dst, v4.String())
			}
		}
	}
	if len(real) > 0 {
		return append(real, virt...)
	}
	return virt
}

// virtualIface 判断接口是不是虚拟机/容器常用的虚拟网卡。
// 只按名字判断，不做网段猜测：网段是用户环境的事，名字是产品自己的约定。
func virtualIface(name string) bool {
	l := strings.ToLower(name)
	for _, k := range []string{"virtualbox", "vbox", "vmware", "vmnet", "hyper-v", "vethernet",
		"docker", "wsl", "tap", "tun", "npcap", "loopback"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// PushEvent 追加一条事件（AI 回复、通知）。
func (s *Server) PushEvent(kind, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.events = append(s.events, Event{Seq: s.seq, Kind: kind, Text: text, At: time.Now()})
	if len(s.events) > MaxEvents {
		s.events = append([]Event{}, s.events[len(s.events)-MaxEvents:]...)
	}
}

// TakeCommands 取走待执行指令。
func (s *Server) TakeCommands(limit int) []Command {
	if limit <= 0 {
		limit = 10
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cmds) == 0 {
		return nil
	}
	if len(s.cmds) < limit {
		limit = len(s.cmds)
	}
	out := append([]Command{}, s.cmds[:limit]...)
	s.cmds = append([]Command{}, s.cmds[limit:]...)
	return out
}

// Pending 待执行指令数。
func (s *Server) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cmds)
}

// Handler 返回 HTTP 处理器（自检用 httptest 直接调）。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/pair", s.handlePair)
	mux.HandleFunc("/api/status", s.auth(s.handleStatus))
	mux.HandleFunc("/api/send", s.auth(s.handleSend))
	mux.HandleFunc("/api/poll", s.auth(s.handlePoll))
	mux.HandleFunc("/api/approvals", s.auth(s.handleApprovals))
	mux.HandleFunc("/api/approve", s.auth(s.handleApprove))
	mux.HandleFunc("/api/convs", s.auth(s.handleConvs))
	mux.HandleFunc("/api/conv", s.auth(s.handleConv))
	mux.HandleFunc("/api/stop", s.auth(s.handleStop))
	mux.HandleFunc("/api/plans", s.auth(s.handlePlans))
	mux.HandleFunc("/api/plan", s.auth(s.handlePlanToggle))
	mux.HandleFunc("/api/devices", s.auth(s.handleDevices))
	mux.HandleFunc("/api/revoke", s.auth(s.handleRevoke))
	mux.HandleFunc("/api/metrics", s.auth(s.handleMetrics))
	return securityHeaders(mux)
}

// securityHeaders 给每个响应统一加安全头：
// nosniff（防 MIME 嗅探）、DENY（防 iframe 点击劫持）、
// no-referrer（token 不会经 Referer 泄漏出去）、
// CSP（只允许同源脚本/样式白名单；内嵌页面无第三方资源）。
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// 页面脚本是内联的：只写 default-src 'self' 会被浏览器按 CSP 拒绝。
		// 每次请求生成一次性 nonce，页面 handler 把它注入 <script>。
		nonce := newCSPNonce()
		csp := "default-src 'self'; style-src 'unsafe-inline'; img-src 'self' data:"
		if nonce != "" {
			csp += "; script-src 'nonce-" + nonce + "'"
			r = r.WithContext(context.WithValue(r.Context(), nonceKey{}, nonce))
		}
		w.Header().Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// auth 包一层 token 校验。
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if !s.validToken(tok) {
			writeJSON(w, 401, map[string]string{"error": "未配对或登录已过期，请重新输入配对码"})
			return
		}
		next(w, r)
	}
}

// bearer 只收 Authorization 头。
// 此前支持 ?token= 查询参数——URL 会进浏览器历史、服务器访问日志、
// 代理日志与截图，配对 token 等于随处可见；本产品没有任何页面需要它，删掉。
func bearer(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// ForgetToken 立即作废一个内存 token（设备被吊销时调用）。
func (s *Server) ForgetToken(tok string) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tokens[tok]; !ok {
		return false
	}
	delete(s.tokens, tok)
	return true
}

func (s *Server) validToken(tok string) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	exp, ok := s.tokens[tok]
	verify := s.deviceVerify
	s.mu.Unlock()
	if ok {
		if time.Now().After(exp) {
			s.mu.Lock()
			delete(s.tokens, tok)
			s.mu.Unlock()
			return false
		}
		return true
	}
	// v1.29：内存表里没有，再查落盘的设备表——重启电脑后手机不该重新扫码。
	if verify != nil {
		return verify(tok)
	}
	return false
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v interface{}) error {
	if r.Method != "POST" {
		return errors.New("只接受 POST")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return errors.New("请求体为空")
	}
	return json.Unmarshal(body, v)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	html := indexHTML
	if nonce, _ := r.Context().Value(nonceKey{}).(string); nonce != "" {
		html = strings.Replace(html, "<script>", `<script nonce="`+nonce+`">`, 1)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, html)
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	ip := clientIP(r)
	now := time.Now()

	s.mu.Lock()
	// 顺手 GC：失败计数/锁定表没有上限，LAN 里换来换去 IP 会让它缓慢无界增长
	if len(s.fails) > 256 || len(s.locked) > 256 {
		for ip, t := range s.locked {
			if now.After(t) {
				delete(s.locked, ip)
			}
		}
		for ip := range s.fails {
			if _, ok := s.locked[ip]; !ok {
				delete(s.fails, ip)
			}
		}
	}
	if until, ok := s.locked[ip]; ok {
		if now.Before(until) {
			left := int(until.Sub(now).Seconds()) + 1
			s.mu.Unlock()
			writeJSON(w, 429, map[string]string{"error": fmt.Sprintf("错误次数过多，请 %d 秒后重试", left)})
			return
		}
		delete(s.locked, ip)
		s.fails[ip] = 0
	}
	if s.code == "" || now.After(s.codeUntil) {
		s.code = randomDigits(6)
		s.codeUntil = now.Add(PairCodeTTL)
	}
	if strings.TrimSpace(req.Code) != s.code {
		s.fails[ip]++
		if s.fails[ip] >= MaxPairFails {
			s.locked[ip] = now.Add(PairLockWindow)
			s.fails[ip] = 0
			s.mu.Unlock()
			writeJSON(w, 429, map[string]string{"error": "错误次数过多，已锁定 60 秒"})
			return
		}
		s.mu.Unlock()
		writeJSON(w, 401, map[string]string{"error": "配对码不正确"})
		return
	}
	tok := randomToken()
	s.tokens[tok] = now.Add(TokenTTL)
	addDevice := s.deviceAdd
	s.code = "" // 一次性：配对成功即作废，防止被旁观者复用
	s.codeUntil = time.Time{}
	s.mu.Unlock()

	// v1.29：token 落盘（只存哈希），重启后仍然有效；名字用手机自报，缺省按 IP 记。
	if addDevice != nil {
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = "手机 " + ip
		}
		addDevice(name, tok)
	}

	st := s.currentStatus()
	writeJSON(w, 200, map[string]interface{}{
		"token":    tok,
		"expires":  now.Add(TokenTTL).Format(time.RFC3339),
		"version":  st.Version,
		"work_dir": st.WorkDir,
	})
}

func (s *Server) currentStatus() Status {
	s.mu.Lock()
	fn := s.statusFn
	pending := len(s.cmds)
	s.mu.Unlock()
	st := Status{}
	if fn != nil {
		st = fn()
	}
	st.Pending = pending
	return st
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.currentStatus())
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeJSON(w, 400, map[string]string{"error": "指令内容为空"})
		return
	}
	if len([]rune(text)) > 4000 {
		writeJSON(w, 400, map[string]string{"error": "指令过长（上限 4000 字）"})
		return
	}

	s.mu.Lock()
	s.cmdSeq++
	cmd := Command{ID: "cmd-" + strconv.Itoa(s.cmdSeq), Text: text, At: time.Now()}
	if len(s.cmds) >= MaxCommands {
		s.mu.Unlock()
		// 队列已满：明确拒绝而不是静默丢弃最旧的一条
		writeJSON(w, 429, map[string]interface{}{
			"error":      "指令队列已满，本条未接收（请等待执行完或 /queue clear）",
			"queue_full": true,
			"queued":     len(s.cmds),
		})
		return
	}
	s.cmds = append(s.cmds, cmd)
	submit := s.submitFn
	s.mu.Unlock()

	// 回调在锁外执行：submit 可能同步唤起引擎，持锁调用会把界面线程与
	// HTTP 线程绑在一起，一个卡住两边都停。
	if submit != nil {
		submit(cmd)
	}
	s.PushEvent("notice", "手机指令已收到："+text)
	writeJSON(w, 200, map[string]string{"id": cmd.ID})
}

func (s *Server) handlePoll(w http.ResponseWriter, r *http.Request) {
	// 长轮询是挂起式请求：并发开几十条会把低配机的 fd/goroutine 都耗光
	if atomic.AddInt32(&s.pollInFlight, 1) > 4 {
		atomic.AddInt32(&s.pollInFlight, -1)
		writeJSON(w, 429, map[string]interface{}{"error": "长轮询并发已达上限，请减少同时在线的指挥台页签"})
		return
	}
	defer atomic.AddInt32(&s.pollInFlight, -1)

	after, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("after")), 10, 64)
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	if wait <= 0 || wait > 25 {
		wait = 15
	}
	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	for {
		s.mu.Lock()
		var out []Event
		for _, e := range s.events {
			if e.Seq > after {
				out = append(out, e)
			}
		}
		s.mu.Unlock()
		if len(out) > 0 || time.Now().After(deadline) {
			writeJSON(w, 200, map[string]interface{}{
				"events": out,
				"status": s.currentStatus(),
			})
			return
		}
		time.Sleep(300 * time.Millisecond) // 长轮询：不占 CPU，也不用 WebSocket
	}
}

package selftest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"win7harness/internal/codeidx"
	"win7harness/internal/core"
	"win7harness/internal/remote"
)

// v1.27 用例：手机指挥台、定时任务、目录守护、更新检查、索引缓存、字号与主题。
//
// 全部用真实数据验证：真实监听端口、真实临时目录、真实 gzip 缓存文件，
// 不接受"编译通过即算通过"。

// ST55 定时任务：排期解析、落盘往返、到期与标记。
func caseScheduleStore(e *env) (Status, string, string) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.Local)
	st := core.LoadSchedules(e.path("sched.json"))

	every, err := st.Add("every 30m", "巡检一次构建", e.workDir, now)
	if err != nil {
		return StatusFail, "间隔排期解析失败", err.Error()
	}
	if every.EverySec != 1800 {
		return StatusFail, "间隔排期换算错误", fmt.Sprintf("every 30m -> %d 秒", every.EverySec)
	}
	if d := every.NextRun.Sub(now); d < 29*time.Minute || d > 31*time.Minute {
		return StatusFail, "下次触发时间不对", fmt.Sprintf("差值 %v", d)
	}

	daily, err := st.Add("daily 09:30", "汇总昨天的改动", e.workDir, now)
	if err != nil {
		return StatusFail, "每日排期解析失败", err.Error()
	}
	if daily.NextRun.Hour() != 9 || daily.NextRun.Minute() != 30 {
		return StatusFail, "每日触发时刻不对", daily.NextRun.Format("15:04")
	}
	if !daily.NextRun.After(now) {
		return StatusFail, "每日排期落在过去", daily.NextRun.Format("2006-01-02 15:04")
	}

	// 低于最小间隔必须被拒绝：30 秒以内的循环会把界面线程和 token 一起烧掉。
	if _, err := st.Add("10s", "太密的循环", e.workDir, now); err == nil {
		return StatusFail, "过密排期未被拦截", "10s 被接受了"
	}

	if err := st.Save(); err != nil {
		return StatusFail, "定时任务落盘失败", err.Error()
	}
	back := core.LoadSchedules(e.path("sched.json"))
	if len(back.Items) != 2 {
		return StatusFail, "回读条数不一致", fmt.Sprintf("写入 2 条，读回 %d 条", len(back.Items))
	}
	if !back.Items[0].Enabled {
		return StatusFail, "新建任务默认未启用", back.Items[0].Describe()
	}

	due := back.Due(now.Add(31 * time.Minute))
	if len(due) != 1 {
		return StatusFail, "到期判定不准", fmt.Sprintf("31 分钟后应到期 1 条，实际 %d 条", len(due))
	}
	back.MarkRun(due[0].ID, now.Add(31*time.Minute))
	if again := back.Due(now.Add(32 * time.Minute)); len(again) != 0 {
		return StatusFail, "执行后仍被判到期", fmt.Sprintf("剩余 %d 条", len(again))
	}
	detail := fmt.Sprintf("every 30m -> %ds，daily 09:30 -> %s，10s 已拒绝，落盘往返 2 条，到期 1 条",
		every.EverySec, daily.NextRun.Format("01-02 15:04"))
	return StatusPass, "定时任务的解析、落盘与到期判定均正确", detail
}

// ST56 目录守护：基线、变化检出、不重复触发、快照落盘。
func caseWatchDiff(e *env) (Status, string, string) {
	dir := e.path("watch")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		return StatusFail, "创建监控目录失败", err.Error()
	}
	if err := writeText(filepath.Join(dir, "src", "a.go"), "package a\n"); err != nil {
		return StatusFail, "写测试文件失败", err.Error()
	}
	if err := writeText(filepath.Join(dir, "src", "b.go"), "package b\n"); err != nil {
		return StatusFail, "写测试文件失败", err.Error()
	}
	if err := writeText(filepath.Join(dir, "note.txt"), "无关文件\n"); err != nil {
		return StatusFail, "写测试文件失败", err.Error()
	}

	st := core.LoadWatches(e.path("watch.json"))
	rule, err := st.Add(dir, "有改动就跑测试", []string{"*.go"}, []string{"node_modules"}, time.Now())
	if err != nil {
		return StatusFail, "守护规则创建失败", err.Error()
	}

	// 第一次 Poll 只建基线，不该触发。
	if fired, _ := st.Poll(rule, time.Now()); fired {
		return StatusFail, "首次快照就误触发", "基线建立阶段报出了变化"
	}

	time.Sleep(30 * time.Millisecond) // 保证 mtime 变化
	if err := writeText(filepath.Join(dir, "src", "a.go"), "package a\n\nfunc A() {}\n"); err != nil {
		return StatusFail, "修改测试文件失败", err.Error()
	}
	if err := writeText(filepath.Join(dir, "src", "c.go"), "package c\n"); err != nil {
		return StatusFail, "新增测试文件失败", err.Error()
	}
	_ = os.Remove(filepath.Join(dir, "note.txt"))

	// v1.28 起改动先并入待触发区，静默期满后的下一轮才真正触发；
	// 这里按真实轮询节奏走两轮（第二轮跳 5 秒，越过 3 秒 debounce）。
	st.Poll(rule, time.Now())
	fired, diff := st.Poll(rule, time.Now().Add(5*time.Second))
	if !fired {
		return StatusFail, "真实改动未触发", fmt.Sprintf("新增 %d 修改 %d 删除 %d",
			len(diff.Added), len(diff.Changed), len(diff.Removed))
	}
	if !strings.Contains(strings.Join(diff.Added, " "), "c.go") {
		return StatusFail, "新增文件未识别", "新增：" + strings.Join(diff.Added, " ")
	}
	if !strings.Contains(strings.Join(diff.Changed, " "), "a.go") {
		return StatusFail, "修改文件未识别", "修改：" + strings.Join(diff.Changed, " ")
	}
	if strings.Contains(strings.Join(diff.Removed, " "), "note.txt") {
		return StatusFail, "忽略规则失效", "note.txt 被当成删除"
	}
	if fired2, _ := st.Poll(rule, time.Now()); fired2 {
		return StatusFail, "无改动仍触发", "同一状态被报了两次"
	}

	if err := st.Save(); err != nil {
		return StatusFail, "守护清单落盘失败", err.Error()
	}
	back := core.LoadWatches(e.path("watch.json"))
	if len(back.Items) != 1 {
		return StatusFail, "守护规则回读失败", fmt.Sprintf("读回 %d 条", len(back.Items))
	}
	if snap, ok := back.Snapshots[back.Items[0].ID]; !ok || len(snap) == 0 {
		return StatusFail, "快照未随规则落盘", "重启后会把全仓判成新增"
	}
	return StatusPass, "守护快照与差异检出正确且不误触发", diff.ChangeSummary()
}

// ST57 手机指挥台：配对鉴权、无效码拒绝、无 token 拦截、指令投递。
func caseRemoteConsole(e *env) (Status, string, string) {
	srv := remote.NewServer()
	defer srv.Stop()
	srv.SetStatus(func() remote.Status {
		return remote.Status{Version: core.Version, Model: "selftest", WorkDir: e.workDir}
	})
	var got []string
	srv.SetSubmit(func(c remote.Command) { got = append(got, c.Text) })

	if _, err := srv.Start("127.0.0.1:0"); err != nil {
		return StatusFail, "无法监听端口——远程安全面未被验证：" + err.Error(), "安全软件或组策略拦截了本程序监听端口，放行后重跑；报告不能算通过"
	}
	// 用真实监听地址拼 base：Start 返回的是给人看的地址（可能被换成局域网 IP），
	// 拿来发请求会拼出 http://http://... 这种怪东西。
	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		return StatusFail, "监听地址不可解析", srv.Addr()
	}
	base := "http://127.0.0.1:" + port

	wrong := "000000"
	if srv.PairCode() == wrong {
		wrong = "111111"
	}
	code, body := doJSON("POST", base+"/api/pair", map[string]string{"code": wrong}, "")
	if code != 401 {
		return StatusFail, "错误配对码未被拒绝", fmt.Sprintf("状态码 %d，响应 %s", code, body)
	}

	if code, _ := doJSON("GET", base+"/api/status", nil, ""); code != 401 {
		return StatusFail, "未鉴权请求未被拦截", fmt.Sprintf("状态码 %d", code)
	}

	code, body = doJSON("POST", base+"/api/pair", map[string]string{"code": srv.PairCode()}, "")
	if code != 200 {
		return StatusFail, "正确配对码未通过", fmt.Sprintf("状态码 %d，响应 %s", code, body)
	}
	var pr struct {
		Token   string `json:"token"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(body), &pr); err != nil || pr.Token == "" {
		return StatusFail, "配对响应缺少 token", body
	}
	if pr.Version != core.Version {
		return StatusFail, "配对响应版本不一致", pr.Version
	}
	if code, body = doJSON("GET", base+"/api/status", nil, pr.Token); code != 200 {
		return StatusFail, "带 token 的状态查询失败", fmt.Sprintf("状态码 %d，响应 %s", code, body)
	}

	code, body = doJSON("POST", base+"/api/send", map[string]string{"text": "跑一下测试"}, pr.Token)
	if code != 200 {
		return StatusFail, "指令发送失败", fmt.Sprintf("状态码 %d，响应 %s", code, body)
	}
	if len(got) != 1 || got[0] != "跑一下测试" {
		return StatusFail, "指令未进入执行队列", fmt.Sprintf("收到 %v", got)
	}
	if srv.Pending() < 1 {
		return StatusFail, "队列未记录指令", fmt.Sprintf("Pending=%d", srv.Pending())
	}
	if code, _ = doJSON("POST", base+"/api/send", map[string]string{"text": "   "}, pr.Token); code != 400 {
		return StatusFail, "空指令未被拒绝", fmt.Sprintf("状态码 %d", code)
	}
	return StatusPass, "配对、鉴权、指令投递全链路正常", "错误码 401、无 token 401、正确码换 token、指令已入队、空指令 400"
}

// ST58 更新检查：清单解析、版本比较、架构匹配、SHA256 校验。
func caseUpdateCheck(e *env) (Status, string, string) {
	// 自检用的是本地 unsigned fixture；正式构建注入了 UpdatePublicKey 时，
	// 这里临时清空公钥，避免 ST58 被自己的验签策略误杀。签名路径由
	// internal/core/update_sign_test.go 覆盖。
	oldKey := core.UpdatePublicKey
	core.UpdatePublicKey = ""
	defer func() { core.UpdatePublicKey = oldKey }()

	// 载荷要大于实现的最小体积阈值（1024 字节），否则会被当成"下错了文件"。
	payload := make([]byte, 4096)
	for i := range payload {
		payload[i] = byte('A' + i%26)
	}
	sum := sha256.Sum256(payload)
	mux := http.NewServeMux()
	mux.HandleFunc("/latest.json", func(w http.ResponseWriter, r *http.Request) {
		url := "http://" + r.Host + "/pkg.exe"
		doc := fmt.Sprintf(`{"version":"9.9.9","notes":"修复若干问题","files":{"amd64":{"url":"%s","sha256":"%s","size":%d}}}`,
			url, hex.EncodeToString(sum[:]), len(payload))
		_, _ = w.Write([]byte(doc))
	})
	mux.HandleFunc("/bad.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{这不是 JSON"))
	})
	mux.HandleFunc("/pkg.exe", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	m, newer, err := core.CheckUpdate("1.27.0", ts.URL+"/latest.json", core.HTTPFetcher)
	if err != nil {
		return StatusFail, "清单拉取失败", err.Error()
	}
	if !newer || m.Version != "9.9.9" {
		return StatusFail, "新版本未识别", fmt.Sprintf("newer=%v version=%s", newer, m.Version)
	}
	if _, newer2, _ := core.CheckUpdate("9.9.9", ts.URL+"/latest.json", core.HTTPFetcher); newer2 {
		return StatusFail, "同版本被误判为可更新", "9.9.9 对比 9.9.9 仍提示更新"
	}
	if _, _, err := core.CheckUpdate("1.27.0", ts.URL+"/bad.json", core.HTTPFetcher); err == nil {
		return StatusFail, "坏清单未报错", "非法 JSON 被当成有效清单"
	}

	f, ok := m.FileFor("amd64")
	if !ok {
		return StatusFail, "amd64 包未匹配", "清单里有 amd64 却匹配不到"
	}
	if _, ok := m.FileFor("arm64"); ok {
		return StatusFail, "不存在的架构被匹配", "arm64 不该命中"
	}
	if _, err := core.DownloadUpdate(f, "amd64", core.HTTPFetcher); err != nil {
		return StatusFail, "正确摘要下载失败", err.Error()
	}
	bad := f
	bad.SHA256 = strings.Repeat("0", 64)
	if _, err := core.DownloadUpdate(bad, "amd64", core.HTTPFetcher); err == nil {
		return StatusFail, "错误摘要未被拦截", "哈希不匹配仍返回成功"
	}
	return StatusPass, "更新检查与下载校验正确",
		fmt.Sprintf("识别 9.9.9，amd64 命中，坏包被摘要校验拦截（%d 字节）", len(payload))
}

// ST59 索引缓存：gzip 落盘、指纹校验、版本不符拒绝使用。
func caseIndexCache(e *env) (Status, string, string) {
	root := e.path("idxsrc")
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		return StatusFail, "创建索引源目录失败", err.Error()
	}
	var src strings.Builder
	src.WriteString("package pkg\n\n")
	for i := 0; i < 120; i++ {
		src.WriteString(fmt.Sprintf("func Fn%03d(a int) int { return a + %d }\n", i, i))
	}
	if err := writeText(filepath.Join(root, "pkg", "big.go"), src.String()); err != nil {
		return StatusFail, "写索引源文件失败", err.Error()
	}

	ix := codeidx.Build(root, nil)
	if ix == nil || len(ix.Syms) == 0 {
		return StatusFail, "索引构建为空", "没有解析出任何符号"
	}
	if ok, reason := ix.CacheValid(); !ok {
		return StatusFail, "新建索引自检不合法", reason
	}

	cache := filepath.Join(e.workDir, "codeindex.json")
	if err := ix.SaveTo(cache); err != nil {
		return StatusFail, "索引缓存写入失败", err.Error()
	}
	raw, err := os.ReadFile(cache)
	if err != nil {
		return StatusFail, "索引缓存读回失败", err.Error()
	}
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		return StatusFail, "缓存未压缩", "文件头不是 gzip 魔数"
	}
	plain, _ := json.Marshal(ix)
	if len(raw) >= len(plain) {
		return StatusFail, "压缩后反而更大", fmt.Sprintf("压缩 %d 字节 / 原始 %d 字节", len(raw), len(plain))
	}

	bad := *ix
	bad.Schema = codeidx.CacheSchema + 1
	if ok, _ := bad.CacheValid(); ok {
		return StatusFail, "schema 不符仍判定有效", "指纹校验失效"
	}
	bad2 := *ix
	bad2.Rules = ix.Rules + "-changed"
	if ok, reason := bad2.CacheValid(); ok || reason != "rules" {
		return StatusFail, "规则版本校验失效", "reason=" + reason
	}
	return StatusPass, "索引缓存压缩与指纹校验均正确",
		fmt.Sprintf("%d 符号，压缩 %d->%d 字节，schema/rules 变更均被拒", len(ix.Syms), len(plain), len(raw))
}

// ST60 字号缩放与主题白名单：配置归一化必须夹住非法值。
func caseScaleTheme(e *env) (Status, string, string) {
	cfg := core.DefaultConfig()
	cfg.Theme = "hc"
	cfg.FontScale = 240
	cfg.Normalize()
	if cfg.Theme != "hc" {
		return StatusFail, "高对比主题被丢弃", cfg.Theme
	}
	if cfg.FontScale != 100 {
		return StatusFail, "越界字号未被夹回", fmt.Sprintf("240 -> %d", cfg.FontScale)
	}
	cfg.FontScale = 130
	cfg.Normalize()
	if cfg.FontScale != 130 {
		return StatusFail, "合法字号被改写", fmt.Sprintf("130 -> %d", cfg.FontScale)
	}
	cfg.Theme = "whatever"
	cfg.Normalize()
	if cfg.Theme == "whatever" {
		return StatusFail, "非法主题未被纠正", cfg.Theme
	}
	cfg.FontScale = 40
	cfg.Normalize()
	if cfg.FontScale != 100 {
		return StatusFail, "过小字号未被夹回", fmt.Sprintf("40 -> %d", cfg.FontScale)
	}
	return StatusPass, "字号与主题的边界处理正确", "hc 保留，240/40 夹回 100，130 保持，非法主题回落"
}

// —— 小工具 ——

func writeText(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// doJSON 发一个请求并读完整响应体。
func doJSON(method, url string, body interface{}, token string) (int, string) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		return 0, err.Error()
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

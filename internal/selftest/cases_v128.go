package selftest

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/remote"
)

// v1.28 用例：指挥台 2.0（二维码配对、会话查看、审批回传、HTTPS、断点续传）
// 与稳定性补强（定时补跑、守护静默窗口、诊断包、配置落盘）。
//
// 一律用真实数据：真实二维码矩阵、真实临时会话文件、真实 TLS 握手、真实 HTTP 往返。
// 二维码不解码而是校验结构不变量（版本-尺寸、定位/时序图案、掩码确定性、格式自洽）：
// 解码要另一套完整实现，而结构错一处就一定扫不出来。

// ST61 二维码结构不变量。
func caseQRStructure(e *env) (Status, string, string) {
	texts := []string{
		"A",
		"http://127.0.0.1:8765/#p=000000",
		"http://192.168.100.200:8765/#p=654321",
		strings.Repeat("x", 120),
		"中文配对 http://192.168.1.7:8765/#p=112233",
	}
	for _, text := range texts {
		q, err := core.EncodeQR(text)
		if err != nil {
			return StatusFail, "二维码编码失败：" + clipStr(text, 20), err.Error()
		}
		if want := 17 + q.Version*4; q.Size != want {
			return StatusFail, "版本与尺寸不匹配",
				fmt.Sprintf("v%d 应为 %dx%d，实际 %d", q.Version, want, want, q.Size)
		}
		// 三个定位图形必须在三个角，且紧邻一圈是白边
		for _, c := range [][2]int{{3, 3}, {q.Size - 4, 3}, {3, q.Size - 4}} {
			if !q.Modules[c[1]][c[0]] {
				return StatusFail, "定位图形缺失",
					fmt.Sprintf("v%d 缺角 (%d,%d)", q.Version, c[0], c[1])
			}
			// 7x7 之外紧邻的一圈必须是白的（分离符）。贴着边角的方向会越界，
			// 所以只检查落在矩阵内的方向，且至少要检查到两个。
			checked := 0
			for _, d := range [][2]int{{4, 0}, {-4, 0}, {0, 4}, {0, -4}} {
				x, y := c[0]+d[0], c[1]+d[1]
				if x < 0 || y < 0 || x >= q.Size || y >= q.Size {
					continue
				}
				if q.Modules[y][x] {
					return StatusFail, "定位图形缺少分离符",
						fmt.Sprintf("v%d (%d,%d)", q.Version, x, y)
				}
				checked++
			}
			if checked < 2 {
				return StatusFail, "无法校验分离符", fmt.Sprintf("v%d 角 (%d,%d)", q.Version, c[0], c[1])
			}
		}
		// 时序图案：第 6 行与第 6 列黑白交替
		for i := 8; i < q.Size-8; i++ {
			want := i%2 == 0
			if q.Modules[6][i] != want || q.Modules[i][6] != want {
				return StatusFail, "时序图案不交替", fmt.Sprintf("v%d 第 %d 位", q.Version, i)
			}
		}
		// 同一输入两次编码必须逐位一致（掩码选择不能带随机）
		q2, _ := core.EncodeQR(text)
		for y := 0; y < q.Size; y++ {
			for x := 0; x < q.Size; x++ {
				if q.Modules[y][x] != q2.Modules[y][x] {
					return StatusFail, "编码结果不确定",
						fmt.Sprintf("v%d (%d,%d) 两次不同", q.Version, x, y)
				}
			}
		}
	}
	return StatusPass, "定位图形、时序图案、版本尺寸与编码确定性均正确",
		"5 组文本覆盖 v1~v8 区段，结构与重复编码一致"
}

// ST62 掩码与罚分：8 种掩码互不相同，自动选择取最低罚分。
func caseQRMaskChoice(e *env) (Status, string, string) {
	text := "http://192.168.1.100:8765/#p=123456"
	var sigs []string
	for m := 0; m < 8; m++ {
		q, err := core.EncodeQRMask(text, m)
		if err != nil {
			return StatusFail, "指定掩码编码失败", fmt.Sprintf("mask %d: %v", m, err)
		}
		if q.Mask != m {
			return StatusFail, "强制掩码未生效", fmt.Sprintf("要求 %d，实际 %d", m, q.Mask)
		}
		var b strings.Builder
		for y := 0; y < q.Size; y++ {
			for x := 0; x < q.Size; x++ {
				if q.Modules[y][x] {
					b.WriteByte('1')
				} else {
					b.WriteByte('0')
				}
			}
		}
		sigs = append(sigs, b.String())
	}
	for i := 0; i < 8; i++ {
		for j := i + 1; j < 8; j++ {
			if sigs[i] == sigs[j] {
				return StatusFail, "不同掩码产生相同矩阵", fmt.Sprintf("mask %d 与 %d 相同", i, j)
			}
		}
	}
	q, _ := core.EncodeQR(text)
	if len(q.Scores) != 8 {
		return StatusFail, "罚分表不完整", fmt.Sprintf("得分项 %d 个", len(q.Scores))
	}
	best := 0
	for i := 1; i < 8; i++ {
		if q.Scores[i] < q.Scores[best] {
			best = i
		}
	}
	if q.Mask != best {
		return StatusFail, "掩码未取最低罚分", fmt.Sprintf("选中 %d，最低是 %d", q.Mask, best)
	}
	return StatusPass, "8 种掩码互不相同，自动选择取最低罚分",
		fmt.Sprintf("选中 mask %d，罚分 %v", q.Mask, q.Scores)
}

// ST63 扫码配对地址：URL 自带一次性配对码，且能编码成二维码。
func casePairURL(e *env) (Status, string, string) {
	srv := remote.NewServer()
	if _, err := srv.Start("127.0.0.1:0"); err != nil {
		return StatusFail, "监听失败", err.Error()
	}
	defer srv.Stop()

	base := srv.BaseURL()
	if !strings.HasPrefix(base, "http://") || !strings.HasSuffix(base, "/") {
		return StatusFail, "根地址格式不对", base
	}
	code := srv.PairCode()
	if len(code) != 6 {
		return StatusFail, "配对码长度不是 6 位", code
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return StatusFail, "配对码含非数字", code
		}
	}
	pair := srv.PairURL()
	if !strings.HasPrefix(pair, base) {
		return StatusFail, "配对地址与根地址不一致", pair
	}
	if !strings.HasSuffix(pair, "#p="+code) {
		return StatusFail, "配对地址未带配对码", pair
	}
	q, err := core.EncodeQR(pair)
	if err != nil {
		return StatusFail, "配对地址无法生成二维码", err.Error()
	}
	return StatusPass, "配对地址带一次性配对码且可生成二维码",
		fmt.Sprintf("%s（码 %s，二维码 v%d %dx%d）", pair, code, q.Version, q.Size, q.Size)
}

// ST64 HTTPS：自签证书覆盖回环与局域网 IP，且真实完成一次 TLS 握手。
func caseRemoteHTTPS(e *env) (Status, string, string) {
	hosts := remote.CertHosts()
	hasLoop := false
	for _, h := range hosts {
		if h == "127.0.0.1" {
			hasLoop = true
		}
	}
	if !hasLoop || len(hosts) < 2 {
		return StatusFail, "证书未覆盖回环地址", fmt.Sprint(hosts)
	}

	certPEM, keyPEM, err := remote.SelfSignedCert(hosts, remote.CertDays)
	if err != nil {
		return StatusFail, "自签证书生成失败", err.Error()
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return StatusFail, "证书与私钥不匹配", err.Error()
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return StatusFail, "证书解析失败", err.Error()
	}
	days := int(time.Until(leaf.NotAfter).Hours() / 24)
	if days < 300 {
		return StatusFail, "证书有效期太短", fmt.Sprintf("剩余 %d 天", days)
	}
	if len(leaf.IPAddresses) == 0 && len(leaf.DNSNames) == 0 {
		return StatusFail, "证书既无 IP 也无域名", "手机浏览器会直接拒绝"
	}

	srv := remote.NewServer()
	addr, err := srv.StartHTTPS("127.0.0.1:0", certPEM, keyPEM)
	if err != nil {
		return StatusFail, "TLS 监听失败", err.Error()
	}
	defer srv.Stop()
	if !srv.HTTPS() {
		return StatusFail, "服务未标记为 HTTPS", addr
	}
	if !strings.HasPrefix(srv.BaseURL(), "https://") {
		return StatusFail, "根地址仍为 http", srv.BaseURL()
	}

	cli := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	resp, err := cli.Get(srv.BaseURL())
	if err != nil {
		return StatusFail, "TLS 请求失败", err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != 200 || !bytes.Contains(bytes.ToLower(body), []byte("<html")) {
		return StatusFail, "TLS 响应异常",
			fmt.Sprintf("状态 %d，正文 %d 字节", resp.StatusCode, len(body))
	}
	return StatusPass, "自签证书覆盖回环与局域网 IP，TLS 握手能拿到配对页",
		fmt.Sprintf("证书剩余 %d 天，监听 %s", days, addr)
}

// ST65 手机看会话：真实会话文件 → 列表 → 增量取行，且非法 ID 被过滤。
func caseRemoteConvView(e *env) (Status, string, string) {
	dir := e.path("convs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return StatusFail, "建会话目录失败", err.Error()
	}
	id := "a1b2c3d4"
	now := time.Now()
	file := map[string]interface{}{
		"v": 1,
		"conv": map[string]interface{}{
			"id":    id,
			"title": "构建失败排查",
			"at":    now,
			"msgs": []map[string]interface{}{
				{"role": "user", "text": "帮我看下编译错误", "at": now},
				{"role": "assistant", "text": "", "at": now,
					"tools": []map[string]string{{"name": "run_command"}}},
				{"role": "assistant", "text": "找到原因：缺少依赖", "at": now},
			},
		},
	}
	raw, _ := json.Marshal(file)
	if err := os.WriteFile(filepath.Join(dir, id+".json"), raw, 0o600); err != nil {
		return StatusFail, "写会话文件失败", err.Error()
	}
	_ = os.WriteFile(filepath.Join(dir, "..evil.json"), []byte("{}"), 0o600)

	srv := remote.NewServer()
	srv.SetSessionDir(dir)
	srv.SetApprovalHub(true)
	addr, err := srv.Start("127.0.0.1:0")
	if err != nil {
		return StatusFail, "监听失败", err.Error()
	}
	defer srv.Stop()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	tok := pairTokenOf(ts.URL, srv)
	if tok == "" {
		return StatusFail, "配对拿不到 token", ts.URL
	}

	list := httpGet(ts.URL+"/api/convs", tok)
	if !strings.Contains(list, id) {
		return StatusFail, "会话列表未包含会话", clipStr(list, 160)
	}
	if strings.Contains(list, "evil") {
		return StatusFail, "非法会话 ID 未被过滤", "..evil.json 出现在列表里，存在越权读文件风险"
	}
	one := httpGet(ts.URL+"/api/conv?id="+id+"&from=0", tok)
	if !strings.Contains(one, "缺少依赖") {
		return StatusFail, "会话内容未返回", clipStr(one, 160)
	}
	tail := httpGet(ts.URL+"/api/conv?id="+id+"&from=3", tok)
	if strings.Contains(tail, "缺少依赖") {
		return StatusFail, "增量取行未按 from 截断", clipStr(tail, 160)
	}
	return StatusPass, "会话列表、内容与增量取行正确，非法 ID 已过滤",
		fmt.Sprintf("3 条消息；from=3 无新增；..evil.json 被忽略（%s）", addr)
}

// ST66 手机审批：手机上点允许后，电脑侧阻塞的确认被放行。
func caseRemoteApproval(e *env) (Status, string, string) {
	srv := remote.NewServer()
	srv.SetApprovalHub(true)
	if _, err := srv.Start("127.0.0.1:0"); err != nil {
		return StatusFail, "监听失败", err.Error()
	}
	defer srv.Stop()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	tok := pairTokenOf(ts.URL, srv)
	if tok == "" {
		return StatusFail, "配对拿不到 token", ts.URL
	}

	type answer struct {
		allow bool
		ok    bool
	}
	done := make(chan answer, 1)
	core.SafeGo("selftest/cases_v128.go:321", func() {
		allow, ok := srv.AskApproval("删除构建产物", "将删除 dist 目录", "confirm", 6*time.Second)
		done <- answer{allow, ok}
	})

	var id string
	for i := 0; i < 60; i++ {
		if pend := srv.PendingApprovals(); len(pend) > 0 {
			id = pend[0].ID
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if id == "" {
		return StatusFail, "审批未进入待办", "PendingApprovals 一直为空"
	}

	body, _ := json.Marshal(map[string]interface{}{"id": id, "allow": true})
	req, _ := http.NewRequest("POST", ts.URL+"/api/approve", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return StatusFail, "提交审批失败", err.Error()
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return StatusFail, "审批接口返回异常", fmt.Sprintf("状态 %d", resp.StatusCode)
	}

	select {
	case r := <-done:
		if !r.ok || !r.allow {
			return StatusFail, "审批未放行", fmt.Sprintf("ok=%v allow=%v", r.ok, r.allow)
		}
	case <-time.After(3 * time.Second):
		return StatusFail, "审批回调超时", "手机已应答但电脑侧没收到"
	}
	return StatusPass, "手机应答可放行电脑侧的高危操作确认", "6 秒窗口内完成一次真实往返"
}

// ST67 断线重连：按 after 续传，不重复也不遗漏。
func caseRemoteResume(e *env) (Status, string, string) {
	srv := remote.NewServer()
	if _, err := srv.Start("127.0.0.1:0"); err != nil {
		return StatusFail, "监听失败", err.Error()
	}
	defer srv.Stop()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	tok := pairTokenOf(ts.URL, srv)
	if tok == "" {
		return StatusFail, "配对拿不到 token", ts.URL
	}

	for i := 1; i <= 5; i++ {
		srv.PushEvent("reply", fmt.Sprintf("第 %d 段输出", i))
	}
	full := httpGet(ts.URL+"/api/poll?after=0&wait=1", tok)
	for i := 1; i <= 5; i++ {
		if !strings.Contains(full, fmt.Sprintf("第 %d 段输出", i)) {
			return StatusFail, "事件缺失", fmt.Sprintf("缺第 %d 段：%s", i, clipStr(full, 200))
		}
	}
	// 手机看到前 3 段后断线，重连 from seq=3
	part := httpGet(ts.URL+"/api/poll?after=3&wait=1", tok)
	if strings.Contains(part, "第 3 段输出") {
		return StatusFail, "续传重复下发旧事件", clipStr(part, 200)
	}
	if !strings.Contains(part, "第 4 段输出") || !strings.Contains(part, "第 5 段输出") {
		return StatusFail, "续传漏发后续事件", clipStr(part, 200)
	}
	// 全部看过之后再拉应当为空，避免页面无限重复追加
	empty := httpGet(ts.URL+"/api/poll?after=5&wait=1", tok)
	if strings.Contains(empty, "段输出") {
		return StatusFail, "追平后仍返回旧事件", clipStr(empty, 200)
	}
	return StatusPass, "断线后按 after 续传，不重复、不遗漏", "after=3 只补发第 4、5 段，after=5 无内容"
}

// ST68 定时补跑：关机期间漏掉的多次合并为一次，并如实报告次数。
func caseScheduleCatchup(e *env) (Status, string, string) {
	start := time.Date(2026, 11, 14, 8, 0, 0, 0, time.Local)
	st := core.LoadSchedules(e.path("sched_catchup.json"))
	sc, err := st.Add("every 30m", "巡检构建", e.workDir, start)
	if err != nil {
		return StatusFail, "加任务失败", err.Error()
	}
	later := start.Add(3 * time.Hour)
	items := st.DueCatchup(later)
	if len(items) != 1 {
		return StatusFail, "补跑条数不对", fmt.Sprintf("期望 1 条，实际 %d 条", len(items))
	}
	if items[0].Missed < 2 {
		return StatusFail, "错过次数未统计", fmt.Sprintf("Missed=%d", items[0].Missed)
	}
	if items[0].Schedule.ID != sc.ID {
		return StatusFail, "补跑任务 ID 不匹配", items[0].Schedule.ID
	}
	st.MarkRun(sc.ID, later)
	if again := st.DueCatchup(later.Add(time.Minute)); len(again) != 0 {
		return StatusFail, "补跑后仍被判到期", fmt.Sprintf("剩余 %d 条", len(again))
	}
	// 没到一个周期就重启不该补跑，否则每次开程序都白跑一次
	st2 := core.LoadSchedules(e.path("sched_catchup2.json"))
	sc2, err := st2.Add("every 30m", "巡检构建", e.workDir, start)
	if err != nil {
		return StatusFail, "加任务失败", err.Error()
	}
	st2.MarkRun(sc2.ID, start)
	if early := st2.DueCatchup(start.Add(5 * time.Minute)); len(early) != 0 {
		return StatusFail, "未到期就触发补跑", fmt.Sprintf("%d 条", len(early))
	}
	return StatusPass, "关机期间的漏跑合并为一次并报出次数，未到期不误触",
		fmt.Sprintf("3 小时漏跑 %d 次，合并补 1 次", items[0].Missed)
}

// ST69 守护静默窗口：窗口内的连续改动合并成一次，且一条都不丢。
func caseWatchQuietGap(e *env) (Status, string, string) {
	dir := e.path("watchgap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return StatusFail, "建目录失败", err.Error()
	}
	st := core.LoadWatches(e.path("watch_gap.json"))
	r, err := st.Add(dir, "有改动就总结", nil, nil, time.Now())
	if err != nil {
		return StatusFail, "加守护失败", err.Error()
	}
	if r.MinGapSec != 30 {
		return StatusFail, "默认静默窗口不是 30 秒", fmt.Sprintf("%d", r.MinGapSec)
	}
	t0 := time.Now()
	if ok, _ := st.Poll(r, t0); ok {
		return StatusFail, "首次建立基线就触发", "会在启动瞬间误报"
	}
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1"), 0o600)
	if ok, _ := st.Poll(r, t0.Add(5*time.Second)); ok {
		return StatusFail, "变化当轮就触发", "应先把变更并入待触发区、等静默期满"
	}
	if ok, _ := st.Poll(r, t0.Add(9*time.Second)); !ok {
		return StatusFail, "改动后未触发", "静默期满仍无触发"
	}
	// 窗口内继续改：合并进 pending，但不能再触发一次
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("2"), 0o600)
	if ok, _ := st.Poll(r, t0.Add(11*time.Second)); ok {
		return StatusFail, "静默窗口内仍触发", "连续改动会反复唤醒模型"
	}
	if ok, _ := st.Poll(r, t0.Add(13*time.Second)); ok {
		return StatusFail, "静默窗口内仍触发", "30 秒窗口内第二次改动被放出来了"
	}
	ok, diff := st.Poll(r, t0.Add(45*time.Second))
	if !ok {
		return StatusFail, "窗口结束未触发", "改动被静默窗口吞掉了"
	}
	if len(diff.Added) == 0 {
		return StatusFail, "触发但没有变更内容", fmt.Sprintf("%+v", diff)
	}
	r.MinGapSec = 0
	_ = os.WriteFile(filepath.Join(dir, "c.txt"), []byte("3"), 0o600)
	if ok, _ := st.Poll(r, t0.Add(50*time.Second)); ok {
		return StatusFail, "关闭窗口后当轮就触发", "仍应先积累变更"
	}
	if ok, _ := st.Poll(r, t0.Add(55*time.Second)); !ok {
		return StatusFail, "关闭静默窗口后未触发", "MinGap=0 应当不设限"
	}
	return StatusPass, "静默窗口内连续改动合并且不丢，设 0 可关闭",
		fmt.Sprintf("10s 不触发、40s 合并触发（新增 %d 个）", len(diff.Added))
}

// ST70 失败自动诊断与指挥台配置落盘。
func caseAutoDiagAndConfig(e *env) (Status, string, string) {
	out := e.path("diag-auto.zip")
	path, err := core.ExportDiag(out, core.DefaultDiagOptions())
	if err != nil {
		return StatusFail, "诊断包导出失败", err.Error()
	}
	info, err := os.Stat(path)
	if err != nil {
		return StatusFail, "诊断包没生成", err.Error()
	}
	if info.Size() < 200 {
		return StatusFail, "诊断包内容过小", fmt.Sprintf("%d 字节", info.Size())
	}
	if !strings.HasSuffix(path, ".zip") {
		return StatusFail, "诊断包不是 zip", path
	}

	cfg := core.DefaultConfig()
	cfg.RemoteEnabled = true
	cfg.RemoteHTTPS = true
	cfg.RemoteApproval = true
	cfg.RemoteApprovalSet = true
	raw, _ := json.Marshal(cfg)
	if !strings.Contains(string(raw), `"remote_https":true`) {
		return StatusFail, "HTTPS 开关未落盘", clipStr(string(raw), 200)
	}
	if !strings.Contains(string(raw), `"remote_approval":true`) {
		return StatusFail, "审批开关未落盘", clipStr(string(raw), 200)
	}
	if !strings.Contains(string(raw), `"remote_approval_set":true`) {
		return StatusFail, "审批开关缺少显式标记", "未配置与配置为关闭无法区分"
	}
	var back core.Config
	if err := json.Unmarshal(raw, &back); err != nil {
		return StatusFail, "配置回读失败", err.Error()
	}
	// 关掉 HTTPS 与手机审批：字段是 omitempty，关掉后不该再出现，且"显式关过"的标记要留下
	back.RemoteHTTPS = false
	back.RemoteApproval = false
	raw2, _ := json.Marshal(back)
	if strings.Contains(string(raw2), `"remote_https":true`) {
		return StatusFail, "HTTPS 关闭未生效", clipStr(string(raw2), 200)
	}
	if strings.Contains(string(raw2), `"remote_approval":true`) {
		return StatusFail, "审批关闭未生效", clipStr(string(raw2), 200)
	}
	if !strings.Contains(string(raw2), `"remote_approval_set":true`) {
		return StatusFail, "关闭后丢失显式标记", "重启后会被当成从未配置"
	}
	return StatusPass, "诊断包可生成，指挥台安全配置可落盘且区分未配置",
		fmt.Sprintf("诊断包 %d 字节，remote_https / remote_approval(_set) 往返正确", info.Size())
}

// —— 用例内部小工具 ——

func clipStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// pairTokenOf 走一次真实配对接口拿 token，不在用例里伪造鉴权。
func pairTokenOf(base string, srv *remote.Server) string {
	body, _ := json.Marshal(map[string]string{"code": srv.PairCode()})
	resp, err := http.Post(base+"/api/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Token
}

func httpGet(url, tok string) string {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "ERR:" + err.Error()
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	cli := &http.Client{Timeout: 8 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return "ERR:" + err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(b)
}

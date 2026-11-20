package selftest

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/coreutil"
	"win7harness/internal/remote"
)

// v1.29 四批能力的自检用例（ST71 起）。
// 原则照旧：不靠"编译通过"当证据，全部用真实端口、真实文件、真实数据跑一遍。

// ST71 手机停止任务：空闲时不许谎报已停，真在跑时要停得掉，未接能力要明说。
func caseRemoteStop(e *env) (Status, string, string) {
	srv := remote.NewServer()
	defer srv.Stop()
	srv.SetStatus(func() remote.Status {
		return remote.Status{Version: core.Version, Model: "selftest", WorkDir: e.workDir}
	})
	running := false
	srv.SetStopHandler(func() (string, bool) {
		if running {
			running = false
			return "已停止当前生成，队列清空", true
		}
		return "当前没有正在跑的任务", false
	})
	if _, err := srv.Start("127.0.0.1:0"); err != nil {
		return StatusFail, "无法监听端口——远程安全面未被验证：" + err.Error(), "安全软件或组策略拦截了本程序监听端口，放行后重跑；报告不能算通过"
	}
	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		return StatusFail, "监听地址不可解析", srv.Addr()
	}
	base := "http://127.0.0.1:" + port
	tok := pairTokenOf(base, srv)
	if tok == "" {
		return StatusFail, "配对失败，无法继续", base
	}

	if code, body := doJSON("GET", base+"/api/stop", nil, tok); code != 405 {
		return StatusFail, "GET 未被拒绝", fmt.Sprintf("%d %s", code, body)
	}
	code, body := doJSON("POST", base+"/api/stop", map[string]string{}, tok)
	if code != 200 {
		return StatusFail, "停止请求失败", fmt.Sprintf("%d %s", code, body)
	}
	var r1 struct {
		Stopped bool   `json:"stopped"`
		Note    string `json:"note"`
	}
	if err := json.Unmarshal([]byte(body), &r1); err != nil {
		return StatusFail, "停止响应不是合法 JSON", body
	}
	if r1.Stopped || !strings.Contains(r1.Note, "没有") {
		return StatusFail, "空闲时谎报已停止", body
	}

	running = true
	code, body = doJSON("POST", base+"/api/stop", map[string]string{}, tok)
	if code != 200 {
		return StatusFail, "运行中停止失败", fmt.Sprintf("%d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &r1); err != nil || !r1.Stopped {
		return StatusFail, "运行中未真停", body
	}
	if running {
		return StatusFail, "停止回调未被调用", "状态仍是运行中"
	}
	if code, _ := doJSON("POST", base+"/api/stop", nil, ""); code != 401 {
		return StatusFail, "未鉴权也能停任务", fmt.Sprintf("状态码 %d", code)
	}

	// 没接停止能力的老宿主：必须明说 503，不能让手机以为停成功了。
	bare := remote.NewServer()
	defer bare.Stop()
	bare.SetStatus(func() remote.Status { return remote.Status{Version: core.Version} })
	if _, err := bare.Start("127.0.0.1:0"); err != nil {
		return StatusFail, "第二个端口未起——远程安全面未被验证：" + err.Error(), "安全软件或组策略拦截了本程序监听端口，放行后重跑；报告不能算通过"
	}
	_, p2, _ := net.SplitHostPort(bare.Addr())
	b2 := "http://127.0.0.1:" + p2
	t2 := pairTokenOf(b2, bare)
	if code, body := doJSON("POST", b2+"/api/stop", map[string]string{}, t2); code != 503 {
		return StatusFail, "未接能力时响应不明确", fmt.Sprintf("%d %s", code, body)
	}
	return StatusPass, "手机可停任务，空闲不谎报，未接能力返回 503",
		"空闲 stopped=false、运行中 stopped=true 且回调生效、GET 405、无 token 401、无能力 503"
}

// ST72 手机计划与设备管理：列表有序、开关落到回调、踢设备要 POST。
func caseRemotePlanDevice(e *env) (Status, string, string) {
	srv := remote.NewServer()
	defer srv.Stop()
	srv.SetStatus(func() remote.Status {
		return remote.Status{Version: core.Version, Model: "selftest", WorkDir: e.workDir}
	})
	srv.SetPlansHook(
		func() []remote.PlanView {
			return []remote.PlanView{
				{ID: "b", Name: "夜间备份", Spec: "每天 23:00", On: true, Runs: 3},
				{ID: "a", Name: "早间摘要", Spec: "每天 08:00", On: false, Runs: 1},
			}
		},
		func(id string, on bool) (bool, string) {
			if id == "a" {
				return true, "早间摘要"
			}
			return false, "没有这条计划"
		},
	)
	revoked := ""
	srv.SetDevicesHook(
		func(callerHash string) []remote.DeviceView {
			return []remote.DeviceView{{ID: "d1", Name: "小马手机", Seen: "11-15 10:00", Expires: "11-22 10:00", Current: callerHash != ""}}
		},
		func(key string) (string, bool) {
			if key == "小马手机" {
				revoked = key
				return key, true
			}
			return "", false
		},
	)
	if _, err := srv.Start("127.0.0.1:0"); err != nil {
		return StatusFail, "无法监听端口——远程安全面未被验证：" + err.Error(), "安全软件或组策略拦截了本程序监听端口，放行后重跑；报告不能算通过"
	}
	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		return StatusFail, "监听地址不可解析", srv.Addr()
	}
	base := "http://127.0.0.1:" + port
	tok := pairTokenOf(base, srv)
	if tok == "" {
		return StatusFail, "配对失败", base
	}

	code, body := doJSON("GET", base+"/api/plans", nil, tok)
	if code != 200 {
		return StatusFail, "计划列表取不到", fmt.Sprintf("%d %s", code, body)
	}
	var pl struct {
		Plans []remote.PlanView `json:"plans"`
	}
	if err := json.Unmarshal([]byte(body), &pl); err != nil || len(pl.Plans) != 2 {
		return StatusFail, "计划列表结构不对", body
	}
	// 排序口径是"按名字升序"（Go 的字符串比较即按码点），中文不是拼音序但必须稳定，
	// 免得每次刷新顺序乱跳。这里用同一口径算出期望值再比对，不预设拼音。
	names := []string{pl.Plans[0].Name, pl.Plans[1].Name}
	sort.Strings(names)
	if pl.Plans[0].Name != names[0] || pl.Plans[1].Name != names[1] {
		return StatusFail, "计划未按名字稳定排序", body
	}

	if code, _ := doJSON("POST", base+"/api/plan", map[string]interface{}{"id": "", "on": true}, tok); code != 400 {
		return StatusFail, "缺 ID 未被拒绝", fmt.Sprintf("状态码 %d", code)
	}
	code, body = doJSON("POST", base+"/api/plan", map[string]interface{}{"id": "a", "on": true}, tok)
	if code != 200 || !strings.Contains(body, `"ok":true`) {
		return StatusFail, "开启计划失败", fmt.Sprintf("%d %s", code, body)
	}
	code, body = doJSON("POST", base+"/api/plan", map[string]interface{}{"id": "zz", "on": true}, tok)
	if code != 200 || !strings.Contains(body, `"ok":false`) {
		return StatusFail, "不存在的计划应返回 ok:false 而不是 500", fmt.Sprintf("%d %s", code, body)
	}

	code, body = doJSON("GET", base+"/api/devices", nil, tok)
	if code != 200 {
		return StatusFail, "设备列表取不到", fmt.Sprintf("%d %s", code, body)
	}
	var dv struct {
		Devices []remote.DeviceView `json:"devices"`
		Tail    string              `json:"current_token_tail"`
	}
	if err := json.Unmarshal([]byte(body), &dv); err != nil || len(dv.Devices) != 1 {
		return StatusFail, "设备列表结构不对", body
	}
	if !dv.Devices[0].Current {
		return StatusFail, "本机设备未被标记为当前设备", body
	}
	if dv.Tail != "" {
		return StatusFail, "不应再返回 token 尾部标识", body
	}

	if code, _ := doJSON("GET", base+"/api/revoke", nil, tok); code != 405 {
		return StatusFail, "GET 吊销未被拒绝", fmt.Sprintf("状态码 %d", code)
	}
	code, body = doJSON("POST", base+"/api/revoke", map[string]string{"key": "小马手机"}, tok)
	if code != 200 || !strings.Contains(body, `"ok":true`) {
		return StatusFail, "吊销失败", fmt.Sprintf("%d %s", code, body)
	}
	if revoked != "小马手机" {
		return StatusFail, "吊销回调未执行", revoked
	}
	return StatusPass, "手机可看计划/开关计划/看设备/踢设备",
		"计划按名排序、缺 ID 400、无效 ID ok:false、当前设备被标记、GET 吊销 405、POST 吊销生效"
}

// ST73 设备表落盘：只存哈希、重启仍认、过期失效、吊销即断、坏文件留证。
func caseDeviceRegistry(e *env) (Status, string, string) {
	path := e.path("v129_devices.json")
	ds := core.LoadDevices(path)
	d1, err := ds.Add("小马手机", "tok-v129-abc", 7*24*time.Hour)
	if err != nil {
		return StatusFail, "登记设备失败", err.Error()
	}
	if d1.TokenHash == "tok-v129-abc" || d1.TokenHash == "" {
		return StatusFail, "token 未做哈希", d1.TokenHash
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return StatusFail, "设备表未落盘", err.Error()
	}
	if strings.Contains(string(raw), "tok-v129-abc") {
		return StatusFail, "设备表里出现 token 明文", clipStr(string(raw), 200)
	}

	// 模拟进程重启：重新读盘后 token 仍可用（v1.28 内存态做不到这点）。
	ds2 := core.LoadDevices(path)
	got, ok := ds2.Verify("tok-v129-abc", time.Now())
	if !ok || got.Name != "小马手机" {
		return StatusFail, "重启后 token 失效", "手机要重新扫码"
	}
	if _, ok := ds2.Verify("tok-乱写", time.Now()); ok {
		return StatusFail, "错误 token 被放行", ""
	}
	if _, ok := ds2.Verify("tok-v129-abc", time.Now().Add(8*24*time.Hour)); ok {
		return StatusFail, "过期 token 仍可用", ""
	}

	// 过期清理：再塞一台已过期设备，Prune 应清掉且不误伤有效设备。
	ds3 := core.LoadDevices(path)
	_, _ = ds3.Add("旧手机", "tok-old", time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if n := ds3.Prune(time.Now()); n != 1 {
		return StatusFail, "过期设备清理数量不对", fmt.Sprintf("%d", n)
	}
	if len(ds3.List()) != 1 {
		return StatusFail, "清理误伤了有效设备", fmt.Sprintf("剩 %d 台", len(ds3.List()))
	}

	// 吊销：按名字即可，返回明文 token 便于同步清内存态。
	if _, ok := ds3.Revoke("小马手机"); !ok {
		return StatusFail, "按名字吊销失败", ""
	}
	if _, ok := ds3.Verify("tok-v129-abc", time.Now()); ok {
		return StatusFail, "吊销后 token 仍可用", "会留下一个踢不掉的手机"
	}

	// 坏文件：留 .bad- 备份，并把原因交给界面。
	bad := e.path("v129_devices_bad.json")
	_ = os.WriteFile(bad, []byte("{坏掉的 json"), 0o600)
	dsBad := core.LoadDevices(bad)
	if dsBad.LoadErr == "" {
		return StatusFail, "坏设备表静默当空表", "用户会以为从没配对过"
	}
	entries, _ := os.ReadDir(e.workDir)
	hasBad := false
	for _, ent := range entries {
		if strings.Contains(ent.Name(), "v129_devices_bad.json.bad-") {
			hasBad = true
		}
	}
	if !hasBad {
		return StatusFail, "坏设备表未留备份", ""
	}

	// 重启后仍然认：新起一个只有落盘校验的回执服务，用旧 token 必须通过。
	srv := remote.NewServer()
	defer srv.Stop()
	srv.SetStatus(func() remote.Status { return remote.Status{Version: core.Version} })
	live := core.LoadDevices(path)
	if _, err := live.Add("小马手机", "tok-live", 24*time.Hour); err != nil {
		return StatusFail, "重新登记失败", err.Error()
	}
	srv.SetDeviceRegistry(func(name, tok string) {}, func(tok string) bool {
		_, ok := live.Verify(tok, time.Now())
		return ok
	})
	if _, err := srv.Start("127.0.0.1:0"); err != nil {
		return StatusFail, "无法监听端口验证持久化鉴权——远程安全面未被验证：" + err.Error(), "安全软件或组策略拦截了本程序监听端口，放行后重跑；报告不能算通过"
	}
	_, port, _ := net.SplitHostPort(srv.Addr())
	base := "http://127.0.0.1:" + port
	if code, body := doJSON("GET", base+"/api/status", nil, "tok-live"); code != 200 {
		return StatusFail, "落盘 token 无法通过鉴权", fmt.Sprintf("%d %s", code, body)
	}
	if code, _ := doJSON("GET", base+"/api/status", nil, "tok-没登记"); code != 401 {
		return StatusFail, "未登记 token 被放行", ""
	}
	return StatusPass, "设备表只存哈希、重启仍认、过期清理、吊销即断、坏文件留证",
		"落盘无明文、重载可验、过期拒绝、Prune 1 台、吊销失效、.bad- 备份、落盘 token 过鉴权"
}

// ST74 运行指标：分位数要算对，/api/metrics 要能吐给手机。
func caseRuntimeMetrics(e *env) (Status, string, string) {
	m := coreutil.NewMetrics(2048)
	for i := 1; i <= 100; i++ {
		m.Observe("tool.read", time.Duration(i)*time.Millisecond, i%10 != 0)
	}
	m.Bump("task.ok")
	snap := m.Snapshot()
	if len(snap.Stats) != 1 {
		return StatusFail, "指标条数不对", fmt.Sprintf("%d", len(snap.Stats))
	}
	st := snap.Stats[0]
	if st.Count != 100 || st.Fail != 10 {
		return StatusFail, "计数不对", fmt.Sprintf("count=%d fail=%d", st.Count, st.Fail)
	}
	if st.P50 < 45 || st.P50 > 55 {
		return StatusFail, "P50 偏差过大", fmt.Sprintf("%dms", st.P50)
	}
	if st.P95 < 90 || st.P95 > 100 {
		return StatusFail, "P95 偏差过大", fmt.Sprintf("%dms", st.P95)
	}
	if snap.HeapMB <= 0 || snap.Goroutines <= 0 {
		return StatusFail, "内存画像读不到", fmt.Sprintf("heap=%d goroutines=%d", snap.HeapMB, snap.Goroutines)
	}
	txt := coreutil.MetricsText(snap)
	if !strings.Contains(txt, "tool.read") || !strings.Contains(txt, "P95") {
		return StatusFail, "/stats 文本缺内容", clipStr(txt, 200)
	}
	if !strings.Contains(txt, "task.ok=1") {
		return StatusFail, "计数器未出现在文本里", clipStr(txt, 200)
	}

	srv := remote.NewServer()
	defer srv.Stop()
	srv.SetStatus(func() remote.Status { return remote.Status{Version: core.Version} })
	srv.SetMetricsHook(func() interface{} { return m.Snapshot() })
	if _, err := srv.Start("127.0.0.1:0"); err != nil {
		return StatusFail, "无法监听端口——远程安全面未被验证：" + err.Error(), "安全软件或组策略拦截了本程序监听端口，放行后重跑；报告不能算通过"
	}
	_, port, _ := net.SplitHostPort(srv.Addr())
	base := "http://127.0.0.1:" + port
	tok := pairTokenOf(base, srv)
	code, body := doJSON("GET", base+"/api/metrics", nil, tok)
	if code != 200 {
		return StatusFail, "手机取指标失败", fmt.Sprintf("%d %s", code, body)
	}
	if !strings.Contains(body, "tool.read") || !strings.Contains(body, "heap_mb") {
		return StatusFail, "手机拿到的指标缺内容", clipStr(body, 300)
	}
	return StatusPass, "耗时/失败/分位数/内存画像可统计，手机能查",
		"100 次采样 P50≈50ms、P95≈95ms、失败 10 次、counters 与 /api/metrics 均可见"
}

// ST75 任务排队：忙时入队不丢，重复触发只进一次，满了要说原因。
func caseTaskQueue(e *env) (Status, string, string) {
	q := core.NewTaskQueue(3)
	if r := q.Push(core.TaskItem{Prompt: "夜间备份", Dedup: "plan-1@23:00", Source: "定时任务"}); !r.OK {
		return StatusFail, "首次入队失败", r.Reason
	}
	if r := q.Push(core.TaskItem{Prompt: "夜间备份", Dedup: "plan-1@23:00", Source: "定时任务"}); r.OK || r.Reason == "" {
		return StatusFail, "同一触发点重复入队未被拒绝", r.Reason
	}
	if r := q.Push(core.TaskItem{Prompt: "目录守护", Dedup: "watch-1@10:00", Source: "目录守护"}); !r.OK {
		return StatusFail, "不同来源入队失败", r.Reason
	}
	if r := q.Push(core.TaskItem{Prompt: "手机指令", Source: "remote"}); !r.OK {
		return StatusFail, "手机指令入队失败", r.Reason
	}
	r := q.Push(core.TaskItem{Prompt: "溢出", Dedup: "plan-9@23:00"})
	if r.OK || !strings.Contains(r.Reason, "满") {
		return StatusFail, "超上限未拒绝或未说明原因", r.Reason
	}
	first, ok := q.Pop()
	if !ok || first.Prompt != "夜间备份" {
		return StatusFail, "未按先后顺序出队", fmt.Sprintf("%+v", first)
	}
	if q.Len() != 2 {
		return StatusFail, "出队后长度不对", fmt.Sprintf("%d", q.Len())
	}
	// 未 Done 之前同键不能再进（防止同一个到期点被反复塞回来）。
	if r := q.Push(core.TaskItem{Prompt: "夜间备份", Dedup: "plan-1@23:00"}); r.OK {
		return StatusFail, "出队即放行同键", "同一到期点会被重复执行"
	}
	q.Done("plan-1@23:00")
	if r := q.Push(core.TaskItem{Prompt: "夜间备份", Dedup: "plan-1@23:00"}); !r.OK {
		return StatusFail, "Done 之后不能再次入队", r.Reason
	}
	if n := q.Clear(); n != 3 {
		return StatusFail, "清空数量不对", fmt.Sprintf("%d", n)
	}
	if _, ok := q.Pop(); ok {
		return StatusFail, "空队列仍能取出任务", ""
	}
	return StatusPass, "忙时任务排队不丢，重复触发去重，满了给出可读原因",
		"上限 3 拒第 4 个、FIFO、未 Done 拒同键、Done 后可再入、Clear 清 3 条"
}

// ST76 更新暂存与架构校验：架构不对不许换，暂存路径要能读回。
func caseUpdateStage(e *env) (Status, string, string) {
	dir := e.path("updates")
	if err := core.PrepareUpdate(dir); err != nil {
		return StatusFail, "更新目录创建失败", err.Error()
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return StatusFail, "更新目录不存在", dir
	}

	x86 := e.path("fake_386.exe")
	amd := e.path("fake_amd64.exe")
	if err := os.WriteFile(x86, fakePE(0x014c), 0o644); err != nil {
		return StatusFail, "写入 386 样本失败", err.Error()
	}
	if err := os.WriteFile(amd, fakePE(0x8664), 0o644); err != nil {
		return StatusFail, "写入 amd64 样本失败", err.Error()
	}
	if a, err := core.ExeArch(x86); err != nil || a != "386" {
		return StatusFail, "386 架构识别错误", fmt.Sprintf("%q %v", a, err)
	}
	if a, err := core.ExeArch(amd); err != nil || a != "amd64" {
		return StatusFail, "amd64 架构识别错误", fmt.Sprintf("%q %v", a, err)
	}
	if err := core.CheckUpdateArch(x86, "386"); err != nil {
		return StatusFail, "架构一致却报错", err.Error()
	}
	if err := core.CheckUpdateArch(x86, "amd64"); err == nil {
		return StatusFail, "架构不一致仍放行", "换错包会直接起不来"
	}
	bad := e.path("fake_bad.exe")
	_ = os.WriteFile(bad, []byte("not a pe"), 0o644)
	if err := core.CheckUpdateArch(bad, "386"); err == nil {
		return StatusFail, "非 PE 文件被当成可替换包", ""
	}

	// 暂存：登记的是即将替换的包，读回必须存在；文件被删了就不再提示。
	if err := core.StageUpdate(dir, amd); err != nil {
		return StatusFail, "暂存失败", err.Error()
	}
	if got := core.PendingUpdateExe(dir); got != amd {
		return StatusFail, "暂存路径读回不一致", got
	}
	_ = os.Remove(amd)
	if got := core.PendingUpdateExe(dir); got != "" {
		return StatusFail, "包已不存在仍报待替换", got
	}

	// 回滚脚本：必须含等待、回滚、放弃三条路径，且不依赖 PowerShell（Win7 可能被策略拦）。
	script := core.ApplyScript(`C:\tools\harness.exe`, `C:\tools\updates\new.exe`, true)
	for _, want := range []string{":wait", ":rollback", ":giveup", `start "" "%TARGET%"`} {
		if !strings.Contains(script, want) {
			return StatusFail, "回滚脚本缺关键片段", want
		}
	}
	if strings.Contains(strings.ToLower(script), "powershell") {
		return StatusFail, "脚本依赖 PowerShell", "Win7 上可能被组策略拦掉"
	}
	if !strings.Contains(script, ".old") {
		return StatusFail, "脚本没有旧文件兜底", "替换失败就回不去了"
	}
	return StatusPass, "更新包架构校验、暂存登记、回滚脚本齐备",
		"386/amd64 识别正确、错架构与非 PE 均拒绝、暂存可读回且包丢失即清除、脚本含 wait/rollback/giveup"
}

// ST77 配置与定时清单损坏：留证据、留原因，不许静默归零。
func caseConfigQuarantine(e *env) (Status, string, string) {
	dir := e.path("v129_cfg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return StatusFail, "建目录失败", err.Error()
	}
	// 定时清单：坏文件要被挪走并留下原因。
	sp := filepath.Join(dir, "schedule.json")
	_ = os.WriteFile(sp, []byte("not json at all"), 0o644)
	st := core.LoadSchedules(sp)
	if st == nil {
		return StatusFail, "坏清单返回 nil", ""
	}
	if st.LoadErr == "" {
		return StatusFail, "坏清单静默清空", "用户会以为定时任务凭空消失"
	}
	if _, err := os.Stat(sp); err == nil {
		return StatusFail, "坏清单没被挪走", "留在原地会被下一次写入覆盖，丢掉排查线索"
	}
	entries, _ := os.ReadDir(dir)
	hasBad := false
	for _, ent := range entries {
		if strings.Contains(ent.Name(), ".bad-") {
			hasBad = true
		}
	}
	if !hasBad {
		return StatusFail, "坏清单未留 .bad- 备份", ""
	}
	// 文件不存在是首次启动的常态，不该报错。
	if got := core.LoadSchedules(filepath.Join(dir, "nope.json")); got == nil || got.LoadErr != "" {
		return StatusFail, "文件不存在时误报错误", fmt.Sprintf("%+v", got)
	}

	// 配置文件走同一条通道：损坏时按默认启动，但原因要能读走一次。
	oldHome, hadHome := os.LookupEnv("WIN7HARNESS_HOME")
	defer func() {
		if hadHome {
			_ = os.Setenv("WIN7HARNESS_HOME", oldHome)
		} else {
			_ = os.Unsetenv("WIN7HARNESS_HOME")
		}
		_ = core.Load() // 恢复真实配置，别让后续用例读到沙箱里的默认值
	}()
	_ = os.Setenv("WIN7HARNESS_HOME", dir)
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte("{坏掉的配置"), 0o644)
	_ = core.TakeConfigLoadError()
	cfg := core.Load()
	if cfg == nil {
		return StatusFail, "坏配置返回 nil", ""
	}
	if cfg.Version == "" {
		return StatusFail, "默认配置没有版本号", ""
	}
	msg := core.TakeConfigLoadError()
	if msg == "" {
		return StatusFail, "配置损坏没留原因", "用户会以为设置被吞了"
	}
	if !strings.Contains(msg, "备份") {
		return StatusFail, "原因里没说明备份到哪", msg
	}
	if again := core.TakeConfigLoadError(); again != "" {
		return StatusFail, "同一原因被反复播报", again
	}
	return StatusPass, "配置与定时清单损坏时留备份、留原因、按默认继续跑",
		"清单坏 → 挪走 + LoadErr；文件缺 → 不报错；配置坏 → 默认启动 + 原因只播一次"
}

// fakePE 造一个只够 ExeArch 识别的最小 PE 头（不必可执行，只验架构判定）。
func fakePE(machine uint16) []byte {
	buf := make([]byte, 4096)
	buf[0], buf[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(buf[0x3c:], 0x80)
	copy(buf[0x80:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(buf[0x84:], machine)
	return buf
}

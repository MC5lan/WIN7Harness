package remote

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"win7harness/internal/core"
)

// v1.29 手机端新增：停止当前任务、定时任务（计划）tab、设备管理、运行指标。
// 全部走同一套 token 鉴权，回调由 app 注入，remote 包不直接碰引擎。

// PlanView 一条定时计划在手机上的样子。
type PlanView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Spec string `json:"spec"`
	On   bool   `json:"on"`
	Next string `json:"next,omitempty"`
	Last string `json:"last,omitempty"`
	Runs int    `json:"runs"`
}

// DeviceView 一台已配对设备。
type DeviceView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Seen    string `json:"seen"`
	Expires string `json:"expires"`
	Current bool   `json:"current"`
}

// SetStopHandler 注入"停止当前任务"的实现。返回（提示语, 是否真的停了）。
func (s *Server) SetStopHandler(fn func() (string, bool)) {
	s.mu.Lock()
	s.stopFn = fn
	s.mu.Unlock()
}

// SetPlansHook 注入计划列表与开关。
func (s *Server) SetPlansHook(list func() []PlanView, toggle func(id string, on bool) (bool, string)) {
	s.mu.Lock()
	s.plansFn = list
	s.planSwitch = toggle
	s.mu.Unlock()
}

// SetDevicesHook 注入设备列表与吊销。
func (s *Server) SetDevicesHook(list func(callerHash string) []DeviceView, revoke func(key string) (string, bool)) {
	s.mu.Lock()
	s.devicesFn = list
	s.revokeFn = revoke
	s.mu.Unlock()
}

// SetDeviceRegistry 注入持久化设备表：配对成功登记、每次请求校验。
// 有了它，手机重启电脑重启都不用重新扫码，也能按名字踢人。
func (s *Server) SetDeviceRegistry(add func(name, tok string), verify func(tok string) bool) {
	s.mu.Lock()
	s.deviceAdd = add
	s.deviceVerify = verify
	s.mu.Unlock()
}

// SetMetricsHook 注入指标快照。
func (s *Server) SetMetricsHook(fn func() interface{}) {
	s.mu.Lock()
	s.metricsFn = fn
	s.mu.Unlock()
}

// handleStop 停掉正在跑的任务。返回具体做了什么，手机端能直接看到结果。
func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "只接受 POST"})
		return
	}
	s.mu.Lock()
	fn := s.stopFn
	s.mu.Unlock()
	if fn == nil {
		writeJSON(w, 503, map[string]string{"error": "本机未提供停止能力"})
		return
	}
	msg, ok := fn()
	if !ok {
		writeJSON(w, 200, map[string]interface{}{"stopped": false, "note": msg})
		return
	}
	s.PushEvent("notice", "手机已请求停止："+msg)
	writeJSON(w, 200, map[string]interface{}{"stopped": true, "note": msg})
}

// handlePlans 列出定时计划。
func (s *Server) handlePlans(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	fn := s.plansFn
	s.mu.Unlock()
	if fn == nil {
		writeJSON(w, 200, map[string]interface{}{"plans": []PlanView{}})
		return
	}
	list := fn()
	sort.SliceStable(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	writeJSON(w, 200, map[string]interface{}{"plans": list})
}

// handlePlanToggle 开关某条计划。
func (s *Server) handlePlanToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "只接受 POST"})
		return
	}
	var req struct {
		ID string `json:"id"`
		On bool   `json:"on"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeJSON(w, 400, map[string]string{"error": "缺少计划 ID"})
		return
	}
	s.mu.Lock()
	fn := s.planSwitch
	s.mu.Unlock()
	if fn == nil {
		writeJSON(w, 503, map[string]string{"error": "本机未提供计划管理"})
		return
	}
	ok, msg := fn(req.ID, req.On)
	if !ok {
		writeJSON(w, 200, map[string]interface{}{"ok": false, "note": msg})
		return
	}
	state := "关闭"
	if req.On {
		state = "开启"
	}
	s.PushEvent("notice", "手机已"+state+"计划："+msg)
	writeJSON(w, 200, map[string]interface{}{"ok": true, "note": msg})
}

// handleDevices 列出已配对设备。
func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	fn := s.devicesFn
	s.mu.Unlock()
	if fn == nil {
		writeJSON(w, 200, map[string]interface{}{"devices": []DeviceView{}})
		return
	}
	cur := bearer(r)
	callerHash := ""
	if cur != "" {
		callerHash = core.HashToken(cur)
	}
	list := fn(callerHash)
	writeJSON(w, 200, map[string]interface{}{"devices": list})
}

// handleRevoke 吊销某台设备（踢下线）。
func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "只接受 POST"})
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	s.mu.Lock()
	fn := s.revokeFn
	s.mu.Unlock()
	if fn == nil {
		writeJSON(w, 503, map[string]string{"error": "本机未提供设备管理"})
		return
	}
	name, ok := fn(strings.TrimSpace(req.Key))
	if !ok {
		writeJSON(w, 200, map[string]interface{}{"ok": false, "note": "没找到这台设备"})
		return
	}
	s.PushEvent("notice", "设备已吊销："+name)
	writeJSON(w, 200, map[string]interface{}{"ok": true, "note": name + " 已下线，需重新扫码"})
}

// handleMetrics Win7 老机最关心"为什么慢"，手机上直接看内存与耗时。
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	fn := s.metricsFn
	s.mu.Unlock()
	if fn == nil {
		writeJSON(w, 200, map[string]interface{}{"metrics": nil})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"metrics": fn(), "at": time.Now().Format(time.RFC3339)})
}

// DescribeStop 生成停止结果提示（app 侧复用）。
func DescribeStop(what string, ok bool) string {
	if ok {
		return fmt.Sprintf("已停止 %s", what)
	}
	return what
}

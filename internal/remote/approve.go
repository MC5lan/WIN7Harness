// 审批回传：电脑上弹的每个确认框，手机上也能点。
//
// 为什么值得做：高危操作确认等的是"人回来"，而人往往就在手机边上。
// 手机点掉就等于把"必须走回电脑"这一步省了——但只对"人已经配对过"的设备开放，
// 且电脑端弹窗始终优先，谁先点算谁的。
package remote

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Approval 一条待审批的请求。
type Approval struct {
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
	Kind   string    `json:"kind,omitempty"` // exec / write / delete / other
}

// ApprovalTimeout 单条审批在手机上等待的上限。超时不影响电脑端弹窗。
const ApprovalTimeout = 10 * time.Minute

// MaxApprovalDetail 详情在手机上最多显示这么多字符：
// 文件列表动辄几百行，全推过去只会让手机卡住。
const MaxApprovalDetail = 1200

type approvalReq struct {
	info Approval
	ch   chan bool
	done bool
}

// SetApprovalHub 打开/关闭手机审批。fn 为 nil 表示不参与。
func (s *Server) SetApprovalHub(on bool) {
	s.mu.Lock()
	s.approvalOn = on
	s.mu.Unlock()
}

// ApprovalHub 手机审批是否开启。
func (s *Server) ApprovalHub() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.approvalOn
}

// AskApproval 把一条确认推到手机，阻塞等待手机答复。
// 返回 (是否允许, 是否由手机给出答复)。未启动/未开启/无人答复时返回 (false,false)，
// 调用方据此继续走电脑端弹窗，不会被手机拖住。
func (s *Server) AskApproval(title, detail, kind string, timeout time.Duration) (bool, bool) {
	if !s.Running() || !s.ApprovalHub() {
		return false, false
	}
	if timeout <= 0 || timeout > ApprovalTimeout {
		timeout = ApprovalTimeout
	}
	req := &approvalReq{
		info: Approval{
			ID:     approvalID(),
			Title:  strings.TrimSpace(title),
			Detail: clipText(detail, MaxApprovalDetail),
			At:     time.Now(),
			Kind:   kind,
		},
		ch: make(chan bool, 1),
	}
	s.mu.Lock()
	if s.approvals == nil {
		s.approvals = map[string]*approvalReq{}
	}
	s.approvals[req.info.ID] = req
	s.mu.Unlock()

	s.PushEvent("ask", "等待确认："+req.info.Title)

	var allow, answered bool
	select {
	case v := <-req.ch:
		allow, answered = v, true
	case <-time.After(timeout):
	}

	s.mu.Lock()
	delete(s.approvals, req.info.ID)
	s.mu.Unlock()

	if answered {
		if allow {
			s.PushEvent("notice", "已在手机上批准："+req.info.Title)
		} else {
			s.PushEvent("notice", "已在手机上拒绝："+req.info.Title)
		}
	} else {
		s.PushEvent("notice", "手机未答复，改由电脑确认："+req.info.Title)
	}
	return allow, answered
}

// PendingApprovals 当前待处理列表（按时间正序）。
func (s *Server) PendingApprovals() []Approval {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Approval, 0, len(s.approvals))
	for _, r := range s.approvals {
		if !r.done {
			out = append(out, r.info)
		}
	}
	return out
}

func approvalID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…（内容过长已截断）"
}

/* ---------------- HTTP ---------------- */

// handleApprovals GET：返回待审批列表。带 wait 时做长轮询，
// 免得手机每两秒空转一次（老手机空转很费电）。
func (s *Server) handleApprovals(w http.ResponseWriter, r *http.Request) {
	wait, _ := atoiDefault(r.URL.Query().Get("wait"), 0)
	if wait > 25 {
		wait = 25
	}
	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	for {
		list := s.PendingApprovals()
		if len(list) > 0 || wait <= 0 || time.Now().After(deadline) {
			writeJSON(w, 200, map[string]interface{}{"approvals": list, "hub": s.ApprovalHub()})
			return
		}
		time.Sleep(400 * time.Millisecond)
	}
}

// handleApprove POST {id, allow}：手机答复。
func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "只接受 POST"})
		return
	}
	var in struct {
		ID    string `json:"id"`
		Allow bool   `json:"allow"`
	}
	if err := readJSON(r, &in); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	s.mu.Lock()
	req := s.approvals[strings.TrimSpace(in.ID)]
	if req == nil {
		s.mu.Unlock()
		// 电脑端已经点过、或已超时：如实告诉手机，别让它以为成功。
		writeJSON(w, 409, map[string]string{"error": "该确认已失效"})
		return
	}
	if req.done {
		s.mu.Unlock()
		// 别的设备先答复了：后到者不占通道，明确告知失败，
		// 此前直接返回 ok:true，后答复者会以为自己的拒绝生效了。
		writeJSON(w, 409, map[string]string{"error": "该确认已被先答复的设备处理"})
		return
	}
	// 只有翻转 done 成功的一方才能投递：ch 缓冲为 1，
	// 此时必然空着（done 只会被翻转一次），发送不会阻塞也不会丢。
	req.done = true
	s.mu.Unlock()
	req.ch <- in.Allow
	writeJSON(w, 200, map[string]interface{}{"ok": true, "allow": in.Allow})
}

// handleConvs GET：会话列表。
func (s *Server) handleConvs(w http.ResponseWriter, r *http.Request) {
	dir := s.SessionDir()
	if dir == "" {
		writeJSON(w, 200, map[string]interface{}{"convs": []interface{}{}})
		return
	}
	list, err := sessionIndex(dir, 30)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"convs": list})
}

// handleConv GET：单个会话的增量内容。
func (s *Server) handleConv(w http.ResponseWriter, r *http.Request) {
	dir := s.SessionDir()
	if dir == "" {
		writeJSON(w, 200, map[string]interface{}{"lines": []interface{}{}, "total": 0})
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	from, _ := atoiDefault(r.URL.Query().Get("from"), 0)
	lines, total, err := sessionLines(dir, id, from, 60)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"lines": lines, "total": total, "id": id})
}

func atoiDefault(s string, def int) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	n := 0
	neg := false
	for i, c := range s {
		if i == 0 && (c == '-' || c == '+') {
			neg = c == '-'
			continue
		}
		if c < '0' || c > '9' {
			return def, fmt.Errorf("不是数字")
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

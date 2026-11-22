//go:build windows

package app

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"win7harness/internal/coreutil"
	"win7harness/internal/ui"
)

// ToolCard 一次工具调用在界面上的呈现。
type ToolCard struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Args   string `json:"args,omitempty"`
	Output string `json:"output,omitempty"`
	// Detail 是只给界面看的结构化细节（edit_file/apply_patch 的 diff），
	// 不进入模型上下文；会话落盘时随 ToolCard 保存，方便回看。
	Detail   string        `json:"detail,omitempty"`
	Done     bool          `json:"done,omitempty"`
	Err      bool          `json:"err,omitempty"`
	Expanded bool          `json:"expanded,omitempty"`
	Started  time.Time     `json:"started"`
	Elapsed  time.Duration `json:"elapsed,omitempty"`
}

// HitView 一条检索命中在界面上的呈现。检索结果必须能点，
// 早先只能记下序号再手打 /open，等于让用户用眼睛记路。
type HitView struct {
	Title   string `json:"title,omitempty"`
	Src     string `json:"src,omitempty"`
	When    string `json:"when,omitempty"`
	Snippet string `json:"snippet,omitempty"`
	ConvIdx int    `json:"conv_idx"`
	MsgIdx  int    `json:"msg_idx"` // -1 表示只命中标题
}

// Message 一条会话消息。
type Message struct {
	Role string `json:"role"` // user / assistant
	// Kind 是界面上的消息类别：notice / budget / error / hits / normal。
	// 旧会话没有该字段时按普通消息处理。
	Kind      string      `json:"kind,omitempty"`
	Text      string      `json:"text,omitempty"`
	Reasoning string      `json:"reasoning,omitempty"`
	Tools     []*ToolCard `json:"tools,omitempty"`
	// Images 用户消息附带的图片（附件目录内的绝对路径），仅用户消息使用。
	Images []string `json:"images,omitempty"`
	// Hits /find 的命中列表，渲染成可点击结果卡。
	Hits      []HitView `json:"hits,omitempty"`
	Streaming bool      `json:"-"`
	Tokens    int       `json:"tokens,omitempty"`
	At        time.Time `json:"at"`
	Err       string    `json:"err,omitempty"`
}

// ToolGroupKey 生成一组折叠工具卡的稳定标识。
// 带上会话 ID：切换会话时同名位置不能互相影响展开状态。
func ToolGroupKey(conv string, msgIdx, firstTool int) string {
	return conv + "|" + fmt.Sprintf("%d|%d", msgIdx, firstTool)
}

// msgFoldOpen 取某条消息内"已展开"的折叠组起始下标集合。
// 没有手动展开过的组不进 map，交给渲染层按默认折叠处理。
func (a *App) msgFoldOpen(msgIdx int, m *Message) map[int]bool {
	if len(a.foldOpen) == 0 || m == nil || len(m.Tools) < 3 {
		return map[int]bool{}
	}
	conv := a.convKey()
	out := make(map[int]bool, 2)
	for i := 0; i < len(m.Tools); i++ {
		if a.foldOpen[ToolGroupKey(conv, msgIdx, i)] {
			out[i] = true
		}
	}
	return out
}

// ToolTitle 返回工具的中文短名。
func ToolTitle(name string) string {
	switch name {
	case "read_file":
		return "读取文件"
	case "write_file":
		return "写入文件"
	case "edit_file":
		return "修改文件"
	case "apply_patch":
		return "应用补丁"
	case "list_dir":
		return "列目录"
	case "glob":
		return "查找文件"
	case "grep":
		return "搜索内容"
	case "run_command":
		return "执行命令"
	case "run_tests":
		return "运行测试"
	case "delete_path":
		return "删除"
	case "todo_write":
		return "更新计划"
	case "code_search":
		return "检索代码"
	case "calls":
		return "查调用关系"
	case "repo_map":
		return "仓库地图"
	case "diagnostics":
		return "编译诊断"
	case "lsp":
		return "语言服务"
	case "toolchain":
		return "工具链检查"
	case "quality":
		return "覆盖与性能"
	case "datatable":
		return "表格查询"
	case "read_image":
		return "读取图片"
	case "read_artifact":
		return "读取完整输出"
	case "web_fetch":
		return "抓取网页"
	case "web_search":
		return "联网搜索"
	case "history_search":
		return "检索历史"
	case "memory_list":
		return "查看记忆"
	case "memory_search":
		return "检索记忆"
	case "memory_save":
		return "保存记忆"
	case "skill_list":
		return "列出技能"
	case "skill_use":
		return "加载技能"
	case "tool_search":
		return "搜索工具"
	case "tool_load":
		return "加载工具"
	case "task":
		return "派发子任务"
	case "task_list":
		return "后台任务"
	case "task_output":
		return "读取任务输出"
	case "task_kill":
		return "终止任务"
	}
	if strings.HasPrefix(name, "mcp_") {
		return "MCP 工具"
	}
	return name
}

// ArgSummary 提取参数中的关键信息用于一行展示。
func ArgSummary(name, raw string) string {
	var m map[string]interface{}
	if json.Unmarshal([]byte(raw), &m) != nil {
		s := strings.TrimSpace(raw)
		if len(s) > 80 {
			s = s[:80]
		}
		return s
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	s := pick("command", "path", "pattern", "url", "query", "name", "description", "prompt")
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	if len([]rune(s)) > 90 {
		s = string([]rune(s)[:90]) + "…"
	}
	return s
}

// HitRowH 检索结果单条卡片的高度（含间距）。
const HitRowH = int32(56)

// itemKind 会话流中的一项类型。
type itemKind int

const (
	itemUser itemKind = iota
	itemAssistant
	itemTool
	itemToolGroup
	itemReasoning
	itemHits
)

type visItem struct {
	kind    itemKind
	msgIdx  int
	toolIdx int
	y, h    int32
	lines   []ui.VisLine
	msg     *Message
	tool    *ToolCard
	// 工具组折叠用：连续多项已完成的工具卡收成一行，
	// 组内失败数与总耗时都由组头给出，点开才逐条铺开。
	groupLen  int
	groupFail int
	groupDur  time.Duration
	// codeBlocks 是本项里各代码块对应的可视行范围与原始文本（复制/保存按钮用）。
	codeBlocks []codeBlockRegion
}

// codeBlockRegion 记录一个 fenced code block 在排版结果里的位置。
type codeBlockRegion struct {
	Block int // ParseMarkdown 里的块下标
	First int // it.lines 里的首行下标
	Last  int // it.lines 里的末行下标（含）
	Lang  string
	Text  string // 原始代码，直接从 Block.Lines 拼，不从高亮后的 Runs 重建
}

// collectCodeBlocks 把解析出的代码块与排版后的可视行对齐。
func collectCodeBlocks(blocks []ui.Block, lines []ui.VisLine) []codeBlockRegion {
	var out []codeBlockRegion
	for bi, b := range blocks {
		if b.Kind != ui.BlockCode || len(b.Lines) == 0 {
			continue
		}
		first, last := -1, -1
		for i, ln := range lines {
			if ln.Block != bi {
				continue
			}
			if first < 0 {
				first = i
			}
			last = i
		}
		if first < 0 {
			continue
		}
		out = append(out, codeBlockRegion{
			Block: bi, First: first, Last: last,
			Lang: b.Lang, Text: strings.Join(b.Lines, "\n"),
		})
	}
	return out
}

// layKey 描述影响排版的全局条件：内容宽度、字号档位、是否显示思考过程。
// 任一项变化就全量重排，否则按消息内容签名做增量复用。
type layKey struct {
	conv  string // 会话标识：换会话必须整段重建，否则会把上一个会话的排版结果套上来
	width int32
	fs    int
	think bool
}

// laySeg 是一段消息的排版结果。items 的 y 为相对该消息原点的偏移，
// y 为该消息在会话坐标系里的起始位置。两者分开后可以只画可视段，
// 不需要每帧把所有消息的可视项摊平成一个大数组。
type laySeg struct {
	sig   uint64
	y     int32
	h     int32
	items []visItem
}

// msgSig 对一条消息的可视内容做快速指纹。只取长度与标志位，
// 不哈希正文字节，避免流式输出时每帧做 O(n) 的字符串运算。
func msgSig(m *Message) uint64 {
	const offset = 1469598103934665603
	const prime = 1099511628211
	h := uint64(offset)
	mix := func(v uint64) {
		h ^= v
		h *= prime
	}
	mix(uint64(len(m.Role)))
	sigText(mix, m.Text)
	mix(uint64(len(m.Text)) * 2654435761)
	mix(uint64(len(m.Reasoning)) * 40503)
	mix(uint64(len(m.Err)))
	mix(uint64(len(m.Tools)) + 1)
	for _, t := range m.Tools {
		mix(uint64(len(t.ID)) + 3)
		mix(uint64(len(t.Name)) + 5)
		mix(uint64(len(t.Args)) + 7)
		mix(uint64(len(t.Output)) + 11)
		mix(uint64(len(t.Detail)) + 13)
		var f uint64
		if t.Done {
			f |= 1
		}
		if t.Err {
			f |= 2
		}
		if t.Expanded {
			f |= 4
		}
		mix(f + 13)
	}
	for _, img := range m.Images {
		sigText(mix, img)
	}
	mix(uint64(len(m.Hits)) + 19)
	for _, hit := range m.Hits {
		mix(uint64(len(hit.Title)) + 23)
		mix(uint64(len(hit.Snippet)) + 29)
		mix(uint64(hit.ConvIdx*131+hit.MsgIdx) + 31)
	}
	if m.Streaming {
		mix(17)
	}
	return h
}

// sigText 采样字符串首尾各 32 字节：只碰固定长度，避免流式输出时每帧扫全文，
// 又能区分"长度相同但内容不同"的替换（编辑重发、压缩上下文后重写）。
func sigText(mix func(uint64), s string) {
	mix(uint64(len(s)) * 2246822519)
	if s == "" {
		return
	}
	const head = 32
	n := len(s)
	if n <= head*2 {
		for i := 0; i < n; i++ {
			mix(uint64(s[i]) + 1)
		}
		return
	}
	for i := 0; i < head; i++ {
		mix(uint64(s[i]) + 1)
	}
	for i := n - head; i < n; i++ {
		mix(uint64(s[i]) + 3)
	}
}

// buildMsgItems 把一条消息排成可视项，y 从 0 开始。
func buildMsgItems(c *ui.Canvas, m *Message, width int32, showThink bool, th *ui.Theme, open map[int]bool) ([]visItem, int32) {
	var items []visItem
	var y int32

	add := func(it visItem) {
		it.y = y
		items = append(items, it)
		y += it.h
	}

	if m.Role == "user" {
		lines := ui.LayoutMarkdown(c, ui.ParseMarkdown(m.Text), width-32, ui.FsBase, th)
		h := ui.LayoutHeight(lines) + 24
		if len(m.Images) > 0 {
			h += ImageRowHeight
		}
		add(visItem{kind: itemUser, h: h, lines: lines, msg: m})
		return items, y
	}

	if m.Reasoning != "" && showThink {
		txt := m.Reasoning
		if !m.Streaming && len([]rune(txt)) > 400 {
			txt = string([]rune(txt)[:400]) + "\n\n…（思考过程已折叠）"
		}
		lines := ui.LayoutMarkdown(c, ui.ParseMarkdown(txt), width-24, ui.FsSm, th)
		add(visItem{kind: itemReasoning, h: ui.LayoutHeight(lines) + 16, lines: lines, msg: m})
	}

	if m.Text != "" || m.Streaming {
		text := m.Text
		if text == "" && m.Streaming {
			text = "…"
		}
		blocks := ui.ParseMarkdown(text)
		lines := ui.LayoutMarkdown(c, blocks, width, ui.FsBase, th)
		add(visItem{kind: itemAssistant, h: ui.LayoutHeight(lines) + 12, lines: lines, msg: m,
			codeBlocks: collectCodeBlocks(blocks, lines)})
	}

	if m.Err != "" {
		lines := ui.LayoutMarkdown(c, ui.ParseMarkdown("**出错了** · "+m.Err), width, ui.FsBase, th)
		add(visItem{kind: itemAssistant, h: ui.LayoutHeight(lines) + 12, lines: lines, msg: m})
	}

	if len(m.Hits) > 0 {
		add(visItem{kind: itemHits, h: int32(len(m.Hits))*HitRowH + 30, msg: m})
	}

	// 折叠分组由 coreutil.FoldRuns 决定：这段判定有单测守着（连续≥3 条已结束才折、
	// 运行中的卡必逐条可见），UI 这里只负责把结果画出来。
	foldAt := map[int]coreutil.FoldRun{}
	if open != nil && len(m.Tools) >= coreutil.FoldMinRun {
		spans := make([]coreutil.ToolSpan, len(m.Tools))
		for i, tc := range m.Tools {
			spans[i] = coreutil.ToolSpan{Done: tc.Done, Fail: tc.Err, Elapsed: tc.Elapsed}
		}
		for _, r := range coreutil.FoldRuns(spans, open) {
			foldAt[r.Start] = r
		}
	}

	for ti := 0; ti < len(m.Tools); {
		t := m.Tools[ti]
		if r, ok := foldAt[ti]; ok {
			add(visItem{
				kind: itemToolGroup, toolIdx: ti, groupLen: r.Len, groupFail: r.Fails,
				groupDur: r.Dur, h: 44, msg: m,
			})
			ti += r.Len
			continue
		}
		h := int32(56)
		if t.Expanded || (!t.Done && t.Output != "") {
			outLines := ui.LayoutMarkdown(c, ui.ParseMarkdown("```\n"+tailLines(t.Output, 24)+"\n```"), width-24, ui.FsSm, th)
			h = 56 + ui.LayoutHeight(outLines) + 8
		}
		if t.Expanded && t.Detail != "" {
			detailLines := ui.LayoutMarkdown(c, ui.ParseMarkdown("```diff\n"+tailLines(t.Detail, 30)+"\n```"), width-24, ui.FsSm, th)
			h += ui.LayoutHeight(detailLines) + 8
		}
		add(visItem{kind: itemTool, toolIdx: ti, h: h + 8, tool: t, msg: m})
		ti++
	}
	return items, y
}

// convKey 返回当前会话的稳定标识，用于判定"是否换了会话"。
func (a *App) convKey() string {
	if cv := a.currentConv(); cv != nil {
		if cv.ID != "" {
			return cv.ID
		}
		return cv.Title + "|" + cv.At.Format("20060102150405.000")
	}
	return ""
}

// layoutSession 把会话排版成可视项列表（坐标为相对会话原点的逻辑像素）。
// 增量策略：按消息缓存分段结果，内容签名不变就整段复用；只有变化的尾段会重新
// 解析 Markdown 与测量字形。长会话（上千条）流式输出时，帧耗时从 O(全部) 降到 O(1)。
func (a *App) layoutSession(c *ui.Canvas) {
	if c == nil {
		return
	}
	pad := int32(48)
	if a.dense() {
		pad = 40
	}
	width := a.layW - pad
	if width < 200 {
		width = 200
	}
	// 字号缩放：测量与绘制必须用同一个系数，否则算出来的高度与实际字形对不上，
	// 表现为"字被切掉半行"或消息之间忽然多出一大片空白。
	if old := c.SetTextScale(a.textScale()); old != a.textScale() {
		a.layKey = layKey{} // 系数变化：旧排版全部作废
	}
	key := layKey{conv: a.convKey(), width: width, fs: a.cfg.FontScale, think: a.showThink}
	full := key != a.layKey
	if full {
		a.layKey = key
		a.laySegs = a.laySegs[:0]
	}

	prev := a.laySegs
	sigs := make([]uint64, len(a.msgs))
	prevSigs := make([]uint64, len(prev))
	for i := range prev {
		prevSigs[i] = prev[i].sig
	}
	for i := range a.msgs {
		sigs[i] = msgSig(&a.msgs[i])
	}
	plan := coreutil.ReusePlan(prevSigs, sigs, full)

	segs := make([]laySeg, len(a.msgs))
	var y int32 = 8
	// 消息之间的间距比行距大一档，读起来才有"一段一段"的节奏。
	// 原来 14px 和行内行距差不多，整屏看上去就是一大坨文字。
	gap := int32(20)
	if a.dense() {
		y, gap = 6, 14
	}

	for mi := range a.msgs {
		m := &a.msgs[mi]
		if m == nil {
			continue
		}
		var seg laySeg
		if plan[mi] && mi < len(prev) {
			seg = prev[mi]
		} else {
			var open map[int]bool
			if !a.cfg.NoFoldTools {
				open = a.msgFoldOpen(mi, m)
			}
			built, h := buildMsgItems(c, m, width, a.showThink, a.th, open)
			seg = laySeg{sig: sigs[mi], h: h, items: built}
		}
		for i := range seg.items {
			seg.items[i].msgIdx = mi
		}
		seg.y = y
		segs[mi] = seg
		y += seg.h + gap
	}

	a.laySegs = segs
	a.contentH = y + 8
	c.SetTextScale(1) // 会话外的界面文字一律按原字号
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	head := lines[:4]
	tail := lines[len(lines)-(n-5):]
	return strings.Join(head, "\n") + fmt.Sprintf("\n…（省略 %d 行）…\n", len(lines)-n+1) + strings.Join(tail, "\n")
}

// drawSession 绘制会话区（clip 为消息区域）。
func (a *App) drawSession(u *UI, clip ui.Rect) {
	c := u.c
	th := a.th
	// 消息内可点击路径/代码块随本帧命中列表一起重建，避免滚出视区后留下陈旧热区。
	a.msgLinks = a.msgLinks[:0]
	a.codeBlocks = a.codeBlocks[:0]
	c.SetTextScale(a.textScale())
	defer c.SetTextScale(1)
	c.Clip(clip)
	defer c.PopClip()

	if len(a.msgs) == 0 {
		a.drawEmptyState(u, clip)
		return
	}

	ox := clip.X + 24
	oy := clip.Y + 8 - int32(a.scroll)

	// 只遍历与视口相交的消息段：长会话从"每帧遍历全部消息"降为
	// "二分找到首段 + 遍历可见段"。段内 items 的 y 是相对坐标。
	first := sort.Search(len(a.laySegs), func(i int) bool {
		seg := a.laySegs[i]
		return oy+seg.y+seg.h >= clip.Y-40
	})
	for si := first; si < len(a.laySegs); si++ {
		seg := a.laySegs[si]
		segTop := oy + seg.y
		if segTop > clip.Bottom()+40 {
			break
		}
		for _, it := range seg.items {
			top := segTop + it.y
			if top > clip.Bottom()+40 || top+it.h < clip.Y-40 {
				continue
			}
			// 跳转高亮：被 /open 或结果卡定位到的消息，短暂描一条强调边，
			// 否则用户看不出自己到底跳到哪儿了。
			if a.hlMsg == it.msgIdx && it.kind != itemHits {
				if el := time.Since(a.hlAt); el < 2600*time.Millisecond {
					fade := 1 - float32(el)/float32(2600*time.Millisecond)
					hr := ui.R(clip.X+10, top-5, clip.W-22, it.h+4)
					c.Round(hr, 8, ui.Alpha(th.Bg, th.Accent, 0.10*fade+0.03))
					c.VLine(hr.X+1, hr.Y+8, hr.H-16, ui.Alpha(th.Bg, th.Accent, 0.9*fade))
				}
			}

			switch it.kind {
			case itemUser:
				// 气泡宽度收紧到内容宽度、整体右对齐。
				//
				// 之前是"占满整行"——用户说的一句话左边顶着边、右边几百像素空白，
				// 这是最典型的"没排过版"信号。真正的聊天界面里，自己说的话是一小块，
				// 右边留白本身就在表达"这是我说的"。
				full := a.chatW - 48
				maxW := int32(0)
				for _, ln := range it.lines {
					if w := ui.LineWidth(c, ln); w > maxW {
						maxW = w
					}
				}
				bw := maxW + 32
				if bw > full*72/100 {
					bw = full * 72 / 100
				}
				if bw < 132 {
					bw = 132
				}
				bx := ox + full - bw
				br := ui.R(bx, top+2, bw, it.h-2)
				c.Round(br, 12, th.UserBubble)
				ui.DrawLines(c, it.lines, bx+16, top+12, clip, th)
				if len(it.msg.Images) > 0 {
					iy := top + 12 + ui.LayoutHeight(it.lines) + 6
					drawImageRow(c, it.msg.Images, bx+16, iy, bw-32, th)
				}
				if !a.streaming {
					a.drawMsgActions(u, it.msgIdx, br, clip, false)
				}

			case itemReasoning:
				r := ui.R(ox, top+2, a.chatW-48, it.h-4)
				c.Round(r, 8, ui.Blend(th.Surface, th.Text, 0.03))
				c.VLine(r.X+8, r.Y+8, r.H-16, ui.Blend(th.TextMute, th.Surface, 0.5))
				// 标题
				c.TextFace(ox+18, top+6, "思考过程", ui.FsXs, 700, th.TextMute, ui.FaceUI)
				ui.DrawLines(c, it.lines, ox+18, top+22, clip, th)

			case itemAssistant:
				ui.DrawLines(c, it.lines, ox+4, top+4, clip, th)
				if it.msg != nil && !it.msg.Streaming && !a.streaming {
					a.drawMsgActions(u, it.msgIdx, ui.R(ox, top, a.chatW-48, it.h), clip, true)
					// 必须排在 drawMsgActions 之后：msgzone 覆盖整条消息且 HitAt 后注册者优先，
					// 否则路径/代码块热区会被 msgzone 吞掉，点了没反应。
					a.drawMsgLinks(u, it, ox+4, top+4, clip)
					a.drawCodeActions(u, it, ox+4, top+4, clip)
				}

			case itemHits:
				a.drawHitList(u, it, ox, top, clip)

			case itemTool:
				a.drawToolCard(u, it, ox, top, clip)

			case itemToolGroup:
				a.drawToolGroup(u, it, ox, top, clip)
			}
		}
	}

	// 流式输出时的脉动光标（取最后一段的最后一项）
	if a.streaming && len(a.laySegs) > 0 {
		lastSeg := a.laySegs[len(a.laySegs)-1]
		if len(lastSeg.items) > 0 {
			last := lastSeg.items[len(lastSeg.items)-1]
			if last.kind == itemAssistant {
				yy := oy + lastSeg.y + last.y + last.h - 8
				alpha := 0.4 + 0.6*float32((1+sin(a.phase*4))/2)
				c.Dot(ox+8, yy, 3, ui.Alpha(th.Accent, th.Accent, alpha))
			}
		}
	}
}

// sin 仅供动画用；直接用标准库 math.Sin（此前的泰勒展开只在 |x|<π 时成立，
// 相位超过 π 就发散——自检页/流式提示的"呼吸感"一直不对）。
func sin(x float32) float32 {
	return float32(math.Sin(float64(x)))
}

// drawToolGroup 绘制折叠后的工具组：一行交代"跑了几次、花了多久、有没有失败"。
// 价值在于长任务里几十条工具调用不再把对话顶得看不见结论，点一下又能逐条查。
func (a *App) drawToolGroup(u *UI, it visItem, ox, top int32, clip ui.Rect) {
	c := u.c
	th := a.th
	r := ui.R(ox, top, a.chatW-48, it.h-6)
	id := fmt.Sprintf("toolgroup:%d:%d", it.msgIdx, it.toolIdx)
	u.Hit(id, r) // 登记命中区
	// hover 过渡：底色随 hoverT 淡入淡出，而不是硬切
	hvt := u.hoverT(id)
	bg := ui.Blend(th.Surface, th.Text, 0.02)
	if hvt > 0 {
		bg = ui.Blend(ui.Blend(th.Surface, th.Text, 0.02), ui.Blend(th.Surface, th.Text, 0.06), hvt)
	}
	c.Round(r, 8, bg)
	gb := th.BorderSoft
	if hvt > 0 {
		gb = ui.Blend(th.BorderSoft, th.Accent, 0.55*hvt)
	}
	c.RoundStroke(r, 8, gb)

	// 左侧图标放在一颗浅强调底的小圆角块里：与正文同字号同字重时，
	// 一整行会糊成列表行；有图标底块才像一张"卡"。
	ictx := ui.R(r.X+9, r.Y+(r.H-24)/2, 24, 24)
	c.Round(ictx, 6, ui.Blend(th.Surface, th.Accent, 0.14))
	drawIcon(c, "layers", ui.R(ictx.X+5, ictx.Y+5, 14, 14), th.Accent)
	label := fmt.Sprintf("已执行 %d 个工具", it.groupLen)
	c.TextFace(ictx.Right()+8, r.Y+(r.H-11)/2, label, ui.FsXs, 700, th.Text, ui.FaceUI)

	// 失败数优先于耗时显示：出错才是用户要一眼看到的信息。
	right := durText(it.groupDur)
	rightColor := th.TextMute
	if it.groupFail > 0 {
		right = fmt.Sprintf("失败 %d · ", it.groupFail) + right
		rightColor = th.Danger
	}
	rw := c.MeasureFace(right, ui.FsXs, 400, ui.FaceUI)
	c.TextFace(r.Right()-24-rw, r.Y+(r.H-11)/2, right, ui.FsXs, 400, rightColor, ui.FaceUI)
	drawIcon(c, "caret-right", ui.R(r.Right()-20, r.Y+(r.H-14)/2, 14, 14), th.TextMute)

	if u.Clicked(id) {
		key := ToolGroupKey(a.convKey(), it.msgIdx, it.toolIdx)
		if a.foldOpen == nil {
			a.foldOpen = map[string]bool{}
		}
		a.foldOpen[key] = !a.foldOpen[key]
		a.layout()
	}
}

// drawToolCard 绘制工具调用卡片。
func (a *App) drawToolCard(u *UI, it visItem, ox, top int32, clip ui.Rect) {
	c := u.c
	th := a.th
	t := it.tool
	r := ui.R(ox, top+2, a.chatW-48, it.h-4)

	id := fmt.Sprintf("tool:%d:%d", it.msgIdx, it.toolIdx)
	hover := u.Hit(id, r)
	hvt := u.hoverT(id)
	// hover 过渡：底色与描边一起淡入，不再只有底色硬切
	bg := th.Card
	if hvt > 0 {
		bg = ui.Blend(th.Card, th.CardHover, hvt)
	}
	c.Round(r, 8, bg)
	border := th.BorderSoft
	if t.Err {
		border = ui.Blend(th.BorderSoft, th.Danger, 0.45)
	}
	if hvt > 0 && !t.Err {
		border = ui.Blend(border, th.Accent, 0.55*hvt)
	}
	c.RoundStroke(r, 8, border)
	// 左侧 3px 状态色竖条：一眼分出成功/失败/进行中，不必盯着状态点颜色猜
	stripe := th.Success
	switch {
	case !t.Done:
		stripe = th.Warn
	case t.Err:
		stripe = th.Danger
	}
	c.Round(ui.R(r.X, r.Y+10, 3, r.H-20), 2, stripe)

	// 状态点
	dotColor := th.TextMute
	if !t.Done {
		dotColor = th.Warn
	} else if t.Err {
		dotColor = th.Danger
	} else {
		dotColor = th.Success
	}
	pulse := float32(1)
	if !t.Done {
		pulse = 0.45 + 0.55*float32((1+sin(a.phase*5))/2)
	}
	c.Dot(r.X+16, r.Y+18, 4, ui.Alpha(bg, dotColor, pulse))

	// 标题与参数摘要
	title := ToolTitle(t.Name)
	c.TextFace(r.X+28, r.Y+11, title, ui.FsSm, 700, th.Text, ui.FaceUI)
	tw := c.MeasureFace(title, ui.FsSm, 700, ui.FaceUI)
	summary := ArgSummary(t.Name, t.Args)
	if summary != "" {
		room := r.W - (28 + tw + 8) - 120
		c.TextFace(r.X+28+tw+8, r.Y+11, ellipsis(c, summary, ui.FsXs, 400, room), ui.FsXs, 400, th.TextMute, ui.FaceMono)
	}

	// 右侧：耗时 + 展开箭头。未记录耗时时不要再显示刺眼的 0.0s。
	right := "运行中"
	rightColor := th.TextMute
	switch {
	case t.Done && t.Err:
		right = "失败 " + durText(t.Elapsed)
		rightColor = th.Danger
	case t.Done:
		right = durText(t.Elapsed)
	}
	rw := c.MeasureFace(right, ui.FsXs, 400, ui.FaceUI)
	c.TextFace(r.Right()-24-rw, r.Y+11, right, ui.FsXs, 400, rightColor, ui.FaceUI)
	if hover {
		if u.Button(fmt.Sprintf("toolcopy:%d:%d", it.msgIdx, it.toolIdx), "复制",
			ui.R(r.Right()-24-rw-56, r.Y+6, 46, 22), BtnSubtle) {
		}
	}
	arrow := "caret-right"
	if t.Expanded {
		arrow = "caret-down"
	}
	drawIcon(c, arrow, ui.R(r.Right()-22, r.Y+10, 14, 14), th.TextMute)

	expanded := t.Expanded || (!t.Done && t.Output != "")
	if expanded {
		outLines := ui.LayoutMarkdown(c, ui.ParseMarkdown("```\n"+tailLines(t.Output, 24)+"\n```"), r.W-24, ui.FsSm, th)
		ui.DrawLines(c, outLines, r.X+12, r.Y+42, clip, th)
		if t.Expanded && t.Detail != "" {
			detailLines := ui.LayoutMarkdown(c, ui.ParseMarkdown("```diff\n"+tailLines(t.Detail, 30)+"\n```"), r.W-24, ui.FsSm, th)
			ui.DrawLines(c, detailLines, r.X+12, r.Y+42+ui.LayoutHeight(outLines)+4, clip, th)
		}
		return
	}
	// 折叠态给一行结果预览：判断"这次调用到底成没成"不该逼用户点开卡片。
	if prev := previewLine(t.Output); prev != "" {
		clr := th.TextMute
		if t.Err {
			clr = ui.Blend(th.TextMute, th.Danger, 0.6)
		}
		c.TextFace(r.X+28, r.Y+32, ellipsis(c, prev, ui.FsXs, 400, r.W-56), ui.FsXs, 400, clr, ui.FaceMono)
	}
}

// drawMsgActions 在消息块右下角浮出操作条：助手消息给"复制/重试"，用户消息给"复制/编辑"。
// 常态不出现，鼠标移上去才浮出，避免整屏按钮噪声。
func (a *App) drawMsgActions(u *UI, mi int, r ui.Rect, clip ui.Rect, assistant bool) {
	if !u.Hit(fmt.Sprintf("msgzone:%d", mi), r) {
		return
	}
	c := u.c
	th := a.th
	const barW, barH = int32(112), int32(24)
	bx := r.Right() - barW
	by := r.Bottom() - barH + 2
	if by+barH+6 > clip.Bottom() {
		by = clip.Bottom() - barH - 6
	}
	if by < clip.Y+4 {
		by = clip.Y + 4
	}
	c.Round(ui.R(bx-8, by-3, barW+14, barH+6), 8, th.Card)
	c.RoundStroke(ui.R(bx-8, by-3, barW+14, barH+6), 8, th.BorderSoft)
	if u.Button(fmt.Sprintf("msgact.copy:%d", mi), "复制", ui.R(bx, by, 50, barH), BtnSubtle) {
	}
	secondID, secondLabel := "msgact.edit", "编辑"
	if assistant {
		secondID, secondLabel = "msgact.retry", "重试"
	}
	if u.Button(fmt.Sprintf("%s:%d", secondID, mi), secondLabel, ui.R(bx+56, by, 50, barH), BtnSubtle) {
	}
}

// durText 格式化耗时段；无记录时给出占位符而不是 0.0s。
func durText(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fm", d.Minutes())
}

// previewLine 取工具输出的第一行有效内容作为折叠态预览。
func previewLine(out string) string {
	for _, ln := range strings.Split(out, "\n") {
		s := strings.TrimSpace(ln)
		if s == "" {
			continue
		}
		if len([]rune(s)) > 96 {
			s = string([]rune(s)[:96]) + "…"
		}
		return s
	}
	return ""
}

// drawHitList 绘制 /find 的可点击结果卡。
func (a *App) drawHitList(u *UI, it visItem, ox, top int32, clip ui.Rect) {
	c := u.c
	th := a.th
	m := it.msg
	w := a.chatW - 48
	for i, h := range m.Hits {
		rr := ui.R(ox, top+2+int32(i)*(HitRowH), w, HitRowH-6)
		hover := u.Hit(fmt.Sprintf("hit:%d:%d", it.msgIdx, i), rr)
		bg := th.Card
		if hover {
			bg = th.CardHover
		}
		c.Round(rr, 8, bg)
		c.RoundStroke(rr, 8, th.BorderSoft)

		// 序号徽章
		badge := ui.R(rr.X+12, rr.Y+12, 24, 24)
		c.Round(badge, 6, ui.Blend(th.Card, th.Accent, 0.16))
		num := fmt.Sprint(i + 1)
		nw := c.MeasureFace(num, ui.FsXs, 700, ui.FaceUI)
		c.TextFace(badge.CX()-nw/2, badge.Y+5, num, ui.FsXs, 700, th.Accent, ui.FaceUI)

		// 右侧来源时间
		meta := h.Src
		if h.When != "" {
			if meta != "" {
				meta += " · "
			}
			meta += h.When
		}
		mw := int32(0)
		if meta != "" {
			mw = c.MeasureFace(meta, ui.FsXs, 400, ui.FaceUI)
			c.TextFace(rr.Right()-12-mw, rr.Y+10, meta, ui.FsXs, 400, th.TextMute, ui.FaceUI)
		}
		// 标题
		titleW := rr.W - 48 - mw - 24
		title := h.Title
		if title == "" {
			title = "新对话"
		}
		c.TextFace(rr.X+46, rr.Y+9, ellipsis(c, title, ui.FsSm, 700, titleW), ui.FsSm, 700, th.Text, ui.FaceUI)
		// 片段
		c.TextFace(rr.X+46, rr.Y+29, ellipsis(c, h.Snippet, ui.FsXs, 400, rr.W-64),
			ui.FsXs, 400, th.TextDim, ui.FaceUI)
	}
	// 底部提示
	c.TextFace(ox+2, top+2+int32(len(m.Hits))*HitRowH+2, "点击任意结果直接跳转，也可输入 /open 序号",
		ui.FsXs, 400, th.TextMute, ui.FaceUI)
}

// drawEmptyState 空会话欢迎区。
func (a *App) drawEmptyState(u *UI, clip ui.Rect) {
	c := u.c
	th := a.th
	c.FillRect(clip, th.Surface)

	cx := clip.CX()
	cy := clip.Y + clip.H/2 - 80

	// 品牌标识（小徽标 + 软光环）
	DrawLogo(c, cx-18, cy-40, 36, th.Accent, th.Surface)

	greet := "你好，今天想做点什么？"
	c.TextFace(cx-c.MeasureFace(greet, ui.FsLg, 700, ui.FaceUI)/2, cy+16,
		greet, ui.FsLg, 700, th.Text, ui.FaceUI)
	sub := "写代码、读文件、执行命令，或者直接聊聊都可以。"
	c.TextFace(cx-c.MeasureFace(sub, ui.FsSm, 400, ui.FaceUI)/2, cy+44,
		sub, ui.FsSm, 400, th.TextDim, ui.FaceUI)

	// 建议卡：2×2，左侧小方图标 + 两行文字，hover 带浮起反馈
	type chip struct{ icon, label, detail, prompt string }
	chips := []chip{
		{"folder", "梳理项目", "扫一遍目录，生成清单", "帮我梳理这个目录里的项目结构，并生成一份清单"},
		{"terminal", "写个工具", "用 Go 写并编译成命令行", "用 Go 写一个命令行待办工具并编译，注意支持 priority 参数"},
		{"edit", "修个报错", "定位根因并直接修复", "分析这段报错，定位根因并直接修复，必要时加测试"},
		{"doc", "补 README", "覆盖构建/运行/结构说明", "给这个项目补一份 README，覆盖如何构建、如何运行、项目结构"},
	}
	gap := int32(12)
	cardW := (520 - gap) / 2
	rowH := int32(76)
	// 卡片必须相对内容区居中：clip 的 X 是聊天区起点，之前漏加 offset，
	// 导致标题居中、卡片却整体偏左，看起来像“欢迎页是歪的”。
	startX := clip.X + (clip.W-(cardW*2+gap))/2
	if startX < clip.X+8 {
		startX = clip.X + 8
	}
	rowY := cy + 64
	for i, chip := range chips {
		col, row := int32(i%2), int32(i/2)
		r := ui.R(startX+col*(cardW+gap), rowY+row*(rowH+gap), cardW, rowH)
		id := fmt.Sprintf("emptychip:%d", i)
		hover := u.Hit(id, r)
		hvt := u.hoverT(id)

		bg := th.Card
		stroke := th.BorderSoft
		if hover {
			bg = ui.Blend(th.Card, th.AccentSoft, 0.22*hvt+0.05)
			stroke = ui.Blend(th.BorderSoft, th.Accent, 0.45*hvt)
		}
		c.Round(r, 12, bg)
		c.RoundStroke(r, 12, stroke)

		iconBox := ui.R(r.X+12, r.Y+(r.H-32)/2, 32, 32)
		iconBg := ui.Blend(th.AccentSoft, th.Accent, 0.12)
		c.Round(iconBox, 7, iconBg)
		drawIcon(c, chip.icon, ui.R(iconBox.X+8, iconBox.Y+8, 16, 16), th.Accent)

		c.TextFace(r.X+52, r.Y+12, chip.label, ui.FsSm, 700, th.Text, ui.FaceUI)
		c.TextFace(r.X+52, r.Y+36, chip.detail, ui.FsXs, 400, th.TextMute, ui.FaceUI)

		if u.Clicked(id) {
			a.input = []rune(chip.prompt)
			a.inputCur = len(a.input)
			a.ui.SetFocus("input")
			a.Redraw()
		}
	}

	// 底部提示：模型 + 快捷键（两列并齐）
	hintY := clip.Bottom() - 40
	hint := "Enter 发送 · Shift+Enter 换行 · / 查看全部命令"
	c.TextFace(cx-c.MeasureFace(hint, ui.FsXs, 400, ui.FaceUI)/2, hintY,
		hint, ui.FsXs, 400, th.TextMute, ui.FaceUI)
}

// DrawLogo 绘制品牌标识：珊瑚圆角方块 + 白色终端提示符（> 与底线光标），
// 象征“装着 AI 的命令行容器”。bg 是背景色（用于在色块上镂空符号）。
func DrawLogo(c *ui.Canvas, x, y, size int32, accent, bg uint32) {
	// 珊瑚圆角方块（半径 = 边长/4，小尺寸也保住圆角感）
	r := size / 4
	if r < 2 {
		r = 2
	}
	c.Round(ui.R(x, y, size, size), r, accent)
	white := bg
	// › 提示符：两笔粗斜线（起点左上、顶点右中、终点左下）
	lx := x + size/4
	lmy := y + size/2
	mx := x + size/2
	sw := size / 5
	if sw < 2 {
		sw = 2
	}
	c.LineW(lx, y+size/4, mx+size/8, lmy, white, sw)
	c.LineW(mx+size/8, lmy, lx, y+size*3/4, white, sw)
	// 底线光标（与提示符一起构成 ">_" 终端意象）
	under := ui.R(lx, y+size*3/4+size/8, size/2, size/8)
	c.Round(under, size/16, white)
}

package agent

import (
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
)

// Diagnosis 是给模型看的"失败诊断"。
//
// 设计动机：v1.19 之前工具失败只回一句 `错误：<原文>`，模型看到"文件不存在"
// 常常直接重试同样的路径，或者开始猜。人排障时会问三个问题：哪一类问题、
// 为什么、下一步做什么。把这三件事在回灌时补上，能省掉大量瞎试。
type Diagnosis struct {
	Kind  string // 分类标识，用于去重与连续失败统计
	Title string // 一句话定性
	Cause string // 可能原因
	Next  string // 建议下一步
}

// diagRules 按顺序匹配，先中者优先。关键词同时覆盖中英文，
// 因为 run_command 的 stderr 来自系统（中文 Win7 也可能夹英文输出）。
var diagRules = []struct {
	kind  string
	title string
	cause string
	next  string
	keys  []string
}{
	{
		kind:  "not_found",
		title: "目标文件或目录不存在",
		cause: "路径拼错、文件被移走，或上一步没真正创建成功；Windows 下还要注意盘符与反斜杠。",
		next:  "先用 list_dir 或 glob 确认实际路径，再按真实名字重试；不要凭记忆猜路径。",
		keys:  []string{"文件不存在", "目录不存在", "no such file", "cannot find the path", "系统找不到指定的路径", "系统找不到指定的文件", "找不到文件", "does not exist"},
	},
	{
		kind:  "permission",
		title: "权限不足，被拒绝",
		cause: "目标在受保护位置、文件被其他程序占用，或当前账户没有写权限。",
		next:  "换到工作区内的路径；确实要写系统位置时先告诉用户需要管理员权限，不要反复重试。",
		keys:  []string{"access is denied", "permission denied", "拒绝访问", "另一个程序正在使用", "being used by another process", "指向工作区之外", "拒绝修改系统保护目录", "拒绝写入系统保护目录", "拒绝删除系统保护目录"},
	},
	{
		kind:  "anchor",
		title: "待替换的原文没匹配上，或匹配不唯一",
		cause: "文件内容已经变了（空格、缩进、换行的细微差异），或给的片段太短命中多处。",
		next:  "先 read_file 读回目标区域看真实内容，再把 old_string 扩到包含前后两行、保证唯一。",
		keys:  []string{"未在", "待替换内容出现了", "不唯一", "old_string 不能为空", "请先用 read_file 确认原文", "无法识别的补丁行", "@@ 出现在文件声明之前"},
	},
	{
		kind:  "command_missing",
		title: "要执行的命令不存在",
		cause: "该命令没装在 PATH 里，或名字拼错；Win7 上常见于 git/node/python 未加入环境变量。",
		next:  "用 where <命令> 确认是否存在；不存在就换等价方案，别重复调用。",
		keys:  []string{"不是内部或外部命令", "command not found", "is not recognized as an internal", "executable file not found", "no such file or directory: exec"},
	},
	{
		kind:  "disk_full",
		title: "磁盘空间不足",
		cause: "目标盘剩余空间不够写完整个文件。",
		next:  "告诉用户清理磁盘或换盘；不要继续写大文件。",
		keys:  []string{"no space left", "磁盘空间不足", "not enough space", "disk full"},
	},
	{
		kind:  "timeout",
		title: "执行超时或被中断",
		cause: "命令跑得太久（安装、编译、下载），或用户点了停止。",
		next:  "把任务拆小或加超时参数分段跑；不要原样再跑一遍。",
		keys:  []string{"timeout", "timed out", "超时", "deadline exceeded", "context canceled", "operation was canceled"},
	},
	{
		kind:  "encoding",
		title: "文件编码和预期不一致",
		cause: "文件可能是 ANSI/GBK 或 UTF-16，按 UTF-8 读会乱码或解析失败。",
		next:  "先确认编码（看有无 BOM），必要时按二进制处理，或提示用户另存为 UTF-8。",
		keys:  []string{"不是 utf-8", "utf-16", "illegal utf", "invalid utf-8", "编码"},
	},
	{
		kind:  "bad_args",
		title: "工具参数不合法",
		cause: "JSON 格式错误、缺必填字段，或类型不对（数字写成字符串等）。",
		next:  "严格按工具 schema 重发一次完整参数；缺字段就补上，不要发空对象试探。",
		keys:  []string{"参数不是合法 json", "缺少必需参数", "参数缺失", "query 与 file 至少给一个", "prompt 或 tasks 至少要有一个", "命令为空", "工作目录为空", "patch 不能为空", "正则非法"},
	},
	{
		kind:  "guard",
		title: "被安全护栏拒绝",
		cause: "操作越过了工作区、命中系统保护目录，或属高危动作但没获得确认。",
		next:  "这不是缺陷：改用工作区内路径，或明确告诉用户需要他确认哪一步，等确认后再继续。",
		keys:  []string{"计划模式", "需用户确认", "pre_tool 钩子阻止"},
	},
	{
		kind:  "too_large",
		title: "目标太大，一次处理不完",
		cause: "文件或结果超出单次读取/输出上限。",
		next:  "用 offset/limit 分段读，或用 grep 精确定位后再读局部。",
		keys:  []string{"文件过大", "请用 offset/limit", "内容过长"},
	},
}

// Diagnose 把一次工具失败归类，给出原因与下一步。
func Diagnose(toolName, errText string) Diagnosis {
	low := strings.ToLower(errText)
	for _, r := range diagRules {
		for _, k := range r.keys {
			if strings.Contains(low, strings.ToLower(k)) {
				return Diagnosis{Kind: r.kind, Title: r.title, Cause: r.cause, Next: r.next}
			}
		}
	}
	// 写盘类失败的兜底：说明"原文件没坏"，避免模型慌乱重写整个文件。
	if strings.Contains(low, "写入失败") || strings.Contains(low, "createfile") {
		return Diagnosis{
			Kind:  "write_failed",
			Title: "写入没成功，原文件保持原样",
			Cause: "磁盘、权限或文件占用导致落盘中断（程序用原子写保护，不会留半截文件）。",
			Next:  "先确认目录可写、目标没被占用，再重试一次；仍失败就如实告诉用户。",
		}
	}
	return Diagnosis{
		Kind:  "unknown",
		Title: "未归类的失败",
		Cause: "错误信息没有命中已知模式。",
		Next:  "把原始错误读一遍再决定下一步；同一调用不要原样重发，先改变一个前提（路径 / 参数 / 工具）。",
	}
}

// Format 生成回灌给模型的文本。原始错误放在最前面，模型与用户都要能看到原文。
func (d Diagnosis) Format(errText string) string {
	var sb strings.Builder
	sb.WriteString(errText)
	sb.WriteString("\n\n[失败诊断] ")
	sb.WriteString(d.Title)
	sb.WriteString("\n- 可能原因：")
	sb.WriteString(d.Cause)
	sb.WriteString("\n- 建议下一步：")
	sb.WriteString(d.Next)
	return sb.String()
}

// failureTracker 记录"同一工具 + 同一诊断类别"的连续失败次数。
//
// 目的是把"失败不盲重试"从提示语变成硬护栏：同一种病根连续三次、且参数一模一样，
// 就拒绝执行，逼模型换方法，而不是把同一行命令刷满上下文。
type failureTracker struct {
	kind string // 诊断类别
	key  string // 上次参数指纹
	n    int    // 同类连续失败次数
	// execBatch 的只读工具走并行 goroutine，本结构会被并发读写——
	// 无锁时计数会错乱（硬护栏该拦不拦/误拦），这里给一把小锁。
	mu sync.Mutex
}

const sameCallHardStop = 3

// check 在下一次调用前判断是否该直接拦下。
func (f *failureTracker) check(key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n >= sameCallHardStop && f.key != "" && f.key == key {
		return "错误：[硬护栏] 同一工具同一参数已连续 " + strconv.Itoa(f.n) +
			" 次因同一原因失败，本次调用被拦下，未执行。请换方法：改参数、换工具，或停下来把情况告诉用户。", true
	}
	return "", false
}

// note 记录一次失败，返回是否需要追加强提示。
func (f *failureTracker) note(toolName, key, errText string) (hint string, stop bool) {
	d := Diagnose(toolName, errText)
	f.mu.Lock()
	defer f.mu.Unlock()
	if d.Kind == "unknown" {
		f.resetLocked()
		return "", false
	}
	if f.kind == d.Kind {
		f.n++
	} else {
		f.kind, f.n = d.Kind, 1
	}
	same := f.key == key && key != ""
	f.key = key

	switch {
	case same && f.n >= sameCallHardStop:
		return "[硬护栏] 这已经是第 " + strconv.Itoa(f.n) + " 次以完全相同的方式失败（同一工具、同一参数、同一病根）。" +
			"继续重发不会改变结果，必须换方法：改参数、换工具，或停下来把情况告诉用户。", true
	case same && f.n == 2:
		return "[提醒] 上一次已经因同样原因失败过。先改变一个前提再试（路径 / 参数 / 工具），不要原样重发。", false
	}
	return "", false
}

// reset 在成功时调用，清空连续失败计数。
func (f *failureTracker) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resetLocked()
}

// resetLocked 已持锁版本。
func (f *failureTracker) resetLocked() { f.kind, f.key, f.n = "", "", 0 }

// argFingerprint 给参数算个短指纹，用来判断"是不是同一发"。
func argFingerprint(args string) string {
	h := fnv.New64a()
	h.Write([]byte(args))
	return strconv.FormatUint(h.Sum64(), 16)
}

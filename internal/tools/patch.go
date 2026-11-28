package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"win7harness/internal/core"
)

// apply_patch：一次落地多处、跨文件的改动。
// 动机：edit_file 一次只能替换一处，Agent 改一个功能常要连打十几次调用，
// 既慢又容易中途失败留下半改状态。补丁式编辑把"一组相关改动"收成一次原子操作。
//
// 补丁格式（与常见编码 Agent 的习惯一致，去掉 *** 前缀也能认）：
//
//	*** Begin Patch
//	*** Update File: internal/core/diff.go
//	@@ 可选定位说明
//	 保持不变的行
//	-要删掉的行
//	+新增的行
//	*** Add File: notes/new.md
//	+第一行
//	*** End Patch
func init() {
	register(Tool{
		Name: "apply_patch",
		Desc: "用一处补丁一次修改多个文件或多段代码（比连续调用 edit_file 更快、更省 token）。补丁以 *** Begin Patch 开头、*** End Patch 结尾，" +
			"每个文件用 *** Update File: 路径 或 *** Add File: 路径 声明，改动用 @@/空格/-/+ 前缀表示。" +
			"建议先传 dry_run=true 预览差异，确认无误后再正式应用。" +
			"补丁里的每一段改动都会编号（#1、#2…），dry_run 的返回里带段落清单；" +
			"只想应用其中几段时传 hunks=\"1,3-4\"，其余段落会被跳过——这就是补丁逐段审阅的落地方式。",
		Schema: `{
  "type":"object",
  "properties":{
    "patch":{
      "type":"string",
      "description":"补丁内容。示例：\n*** Begin Patch\n*** Update File: main.go\n@@ func main\n-\tfmt.Println(\"old\")\n+\tfmt.Println(\"new\")\n*** End Patch"
    },
    "dry_run":{"type":"boolean","description":"只校验并返回差异预览，不写盘。默认 false"},
    "hunks":{"type":"string","description":"只应用选中的段落编号，如 \"1,3-4\"；留空表示全部应用"}
  },
  "required":["patch"]
}`,
		Run: runApplyPatch,
	})
}

// PatchPaths 从补丁正文里提取涉及的文件路径，供自动验证定位受影响包。
// 只做轻量行扫描，不替代 parsePatch 的完整校验。
func PatchPaths(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"*** Update File:", "*** Add File:", "*** Delete File:"} {
			if strings.HasPrefix(line, prefix) {
				p := strings.TrimSpace(strings.TrimPrefix(line, prefix))
				if p != "" {
					out = append(out, p)
				}
				break
			}
		}
	}
	return out
}

// patchHunk 是一段连续改动，lines 保留前缀（' ' / '-' / '+'）。
type patchHunk struct {
	at    string
	lines []string
}

// filePatch 是单个文件的改动集合。
type filePatch struct {
	path  string
	add   bool
	hunks []patchHunk
}

func runApplyPatch(ctx *Context, args map[string]interface{}) (Result, error) {
	body := Get(args, "patch")
	dry := GetBool(args, "dry_run", false)
	if strings.TrimSpace(body) == "" {
		return Result{}, fmt.Errorf("patch 不能为空")
	}
	patches, err := parsePatch(body)
	if err != nil {
		return Result{}, err
	}
	if len(patches) == 0 {
		return Result{}, fmt.Errorf("补丁里没有任何 *** Update File / *** Add File 段落")
	}
	catalog := hunkCatalog(patches)

	patches, early, err := selectPatchHunks(args, patches, catalog)
	if err != nil {
		return Result{}, err
	}
	if early != nil {
		return *early, nil
	}

	patches, uiConfirmed, err := confirmPatchSelection(ctx, dry, patches)
	if err != nil {
		return Result{}, err
	}

	previews, writes, changed, err := buildPatchWrites(ctx, patches)
	if err != nil {
		return Result{}, err
	}

	return commitPatchWrites(ctx, dry, catalog, previews, writes, changed, uiConfirmed)
}

// selectPatchHunks 按 hunks 参数筛选补丁段落。early 非空表示无需继续写盘。
func selectPatchHunks(args map[string]interface{}, patches []filePatch, catalog hunkCatalogT) ([]filePatch, *Result, error) {
	// 逐段审阅：先给全补丁里的每一段改动编号，再按 hunks 参数筛掉不要的段。
	if selText := strings.TrimSpace(Get(args, "hunks")); selText != "" {
		keep, perr := parseHunkSel(selText, len(catalog.refs))
		if perr != nil {
			return nil, nil, perr
		}
		filtered := make([]filePatch, len(patches))
		for i := range patches {
			filtered[i].path = patches[i].path
			filtered[i].add = patches[i].add
		}
		for idx, item := range catalog.refs {
			if !keep[idx] {
				continue
			}
			filtered[item.file].hunks = append(filtered[item.file].hunks, patches[item.file].hunks[item.hunk])
		}
		// 段被全部排除的文件直接略过，避免产生"空改动"噪音。
		var compact []filePatch
		for _, fp := range filtered {
			if len(fp.hunks) > 0 {
				compact = append(compact, fp)
			}
		}
		patches = compact
		selected := 0
		for _, v := range keep {
			if v {
				selected++
			}
		}
		if len(patches) == 0 {
			return nil, &Result{Output: fmt.Sprintf("选中的 %d 段改动都不存在或被跳过，未写盘。\n\n段落清单：\n%s", selected, catalog.text())}, nil
		}
	}
	return patches, nil, nil
}

// confirmPatchSelection 把补丁段落交给 UI 逐段审阅，返回过滤后的补丁。
func confirmPatchSelection(ctx *Context, dry bool, patches []filePatch) ([]filePatch, bool, error) {
	uiConfirmed := false
	// 界面逐段审阅：把每段补丁的真实差异交给 UI，用户可单独接受/拒绝。
	// 这里复用上面模型的 hunks 筛选逻辑——把用户选中的段落重新过滤一遍即可。
	if ctx.ConfirmDiff != nil && !dry {
		remaining := hunkCatalog(patches)
		if len(remaining.refs) > 0 {
			uiHunks := make([]core.DiffHunk, 0, len(remaining.refs))
			for idx, ref := range remaining.refs {
				fp := patches[ref.file]
				abs := core.ResolvePath(ctx.WorkDir, fp.path)
				if core.ValidateWindowsPathSyntax(abs) != nil {
					continue
				}
				// UNC 的 SMB 连接不能在这里提前触发；buildPatchWrites 会
				// 在 guardEscapes 里统一让用户确认。
				if core.IsUNCPath(abs) {
					continue
				}
				oldText, _, e := readTextFile(abs)
				if e != nil {
					continue
				}
				newText, e := applyHunks(oldText, []patchHunk{fp.hunks[ref.hunk]})
				if e != nil {
					continue
				}
				hs := core.DiffHunks(oldText, newText, fp.path, 1)
				if len(hs) == 0 {
					continue
				}
				h := hs[0] // 一段补丁通常就是一个连续改动
				h.Title = fmt.Sprintf("#%d  %s", idx+1, fp.path)
				uiHunks = append(uiHunks, h)
			}
			accepted := ctx.ConfirmDiff("应用补丁前确认（逐段审阅）", "补丁", uiHunks, true)
			if accepted == nil {
				return nil, false, fmt.Errorf("用户取消了补丁，本次补丁全部未生效")
			}
			keep := make([]bool, len(remaining.refs))
			for _, i := range accepted {
				if i >= 0 && i < len(keep) {
					keep[i] = true
				}
			}
			filtered := make([]filePatch, len(patches))
			for i := range patches {
				filtered[i].path, filtered[i].add = patches[i].path, patches[i].add
			}
			for idx, ref := range remaining.refs {
				if keep[idx] {
					filtered[ref.file].hunks = append(filtered[ref.file].hunks, patches[ref.file].hunks[ref.hunk])
				}
			}
			var compact []filePatch
			for _, fp := range filtered {
				if len(fp.hunks) > 0 {
					compact = append(compact, fp)
				}
			}
			patches = compact
			uiConfirmed = true
		}
	}
	return patches, uiConfirmed, nil
}

// buildPatchWrites 校验路径与内容，生成待写文件与预览。
func buildPatchWrites(ctx *Context, patches []filePatch) ([]string, []patchWrite, int, error) {
	var previews []string
	var writes []patchWrite
	changed := 0

	for _, fp := range patches {
		p := core.ResolvePath(ctx.WorkDir, fp.path)
		if err := core.ValidateWindowsPathSyntax(p); err != nil {
			return nil, nil, 0, err
		}
		if err := guardEscapes(ctx, p, "修改"); err != nil {
			return nil, nil, 0, err
		}
		if core.IsForbiddenPath(p) {
			return nil, nil, 0, fmt.Errorf("拒绝修改系统保护目录: %s", p)
		}
		if other, ok := core.CaseInsensitiveCollision(p); ok {
			return nil, nil, 0, fmt.Errorf("目标与已有文件仅大小写不同：%s 已存在；请改用原大小写", other)
		}

		if fp.add {
			if _, serr := os.Stat(p); serr == nil {
				return nil, nil, 0, fmt.Errorf("%s 已存在，改用 *** Update File 修改它", p)
			}
			var buf []string
			for _, h := range fp.hunks {
				for _, l := range h.lines {
					if strings.HasPrefix(l, "-") {
						continue
					}
					if strings.HasPrefix(l, "+") || strings.HasPrefix(l, " ") {
						buf = append(buf, l[1:])
					}
				}
			}
			text := strings.Join(buf, "\n")
			if text != "" {
				text += "\n"
			}
			meta := defaultTextMeta(p)
			if meta.EOL == "" {
				meta.EOL = detectEOL(normalizeForMatch(text))
			}
			previews = append(previews, core.UnifiedDiff("", normalizeForMatch(text), fp.path+"（新建）", 3))
			writes = append(writes, patchWrite{p, text, meta})
			changed++
			continue
		}

		oldText, meta, rerr := readTextFile(p)
		if rerr != nil {
			if errors.Is(rerr, errBinaryText) {
				return nil, nil, 0, fmt.Errorf("%s 是二进制文件，apply_patch 只支持文本", filepath.Base(p))
			}
			return nil, nil, 0, fmt.Errorf("读取失败 %s: %v", p, rerr)
		}
		newText, aerr := applyHunks(oldText, fp.hunks)
		if aerr != nil {
			return nil, nil, 0, fmt.Errorf("%s: %v", fp.path, aerr)
		}
		if newText == oldText {
			previews = append(previews, fp.path+"：补丁未产生任何变化\n")
			continue
		}
		previews = append(previews, core.UnifiedDiff(normalizeForMatch(oldText), normalizeForMatch(newText), fp.path, 3))
		writes = append(writes, patchWrite{p, newText, meta})
		changed++
	}
	return previews, writes, changed, nil
}

// commitPatchWrites 全量编码、确认、写盘，失败时回滚已写文件。
func commitPatchWrites(ctx *Context, dry bool, catalog hunkCatalogT, previews []string, writes []patchWrite, changed int, uiConfirmed bool) (Result, error) {
	summary := strings.Join(previews, "\n")
	head := catalog.text()
	detail := clipForTool(summary, 12000)
	if dry {
		return Result{Output: fmt.Sprintf("补丁校验通过（dry_run，未写盘）：%d 个文件待改动\n\n段落清单：\n%s\n\n%s", changed, head, summary), Detail: detail}, nil
	}
	if changed == 0 {
		return Result{Output: "补丁校验通过，但没有产生任何实际改动\n\n段落清单：\n" + head + "\n\n" + summary, Detail: detail}, nil
	}

	// 先全部编码成功，再开始确认/写盘：避免写到第 3 个文件时才发现
	// 新内容无法用 GBK 表示，留下前两个文件已改的半成品。
	encoded := make([][]byte, len(writes))
	for i, w := range writes {
		data, eerr := encodeText(w.text, w.meta)
		if eerr != nil {
			return Result{}, fmt.Errorf("%s: %v（本次补丁全部未生效）", filepath.Base(w.path), eerr)
		}
		encoded[i] = data
	}

	// 逐个文件确认并写盘。任一文件被用户拒绝则整体不落盘，
	// 保证"要么全改，要么全不改"，避免留下半成品。
	if !uiConfirmed {
		for i, w := range writes {
			oldText := ""
			if text, _, err := readTextFile(w.path); err == nil {
				oldText = text
			}
			if !confirmDiff(ctx, w.path, normalizeForMatch(oldText), normalizeForMatch(w.text), "应用补丁") {
				return Result{}, fmt.Errorf("用户取消了补丁写入（第 %d 个文件 %s），本次补丁全部未生效", i+1, filepath.Base(w.path))
			}
		}
	}

	var done []string
	// 补丁是"全有或全无"的语义：写到一半失败必须把已写的退回去，
	// 否则代码会停在改了一半的状态——比没改更糟。
	// 回滚逐文件检查结果：失败的不再声称"已全部回滚"，
	// 直接列出没退回去的文件让用户自己收尾。
	rollback := func() []string {
		var failed []string
		for i := len(done) - 1; i >= 0; i-- {
			if ok, err := core.RestoreLatestFor(done[i]); err != nil || !ok {
				failed = append(failed, done[i])
			}
		}
		return failed
	}
	writeErr := func(cause error) error {
		if failed := rollback(); len(failed) > 0 {
			return fmt.Errorf("%v；回滚时以下文件未能还原：%s（请手动检查）", cause, strings.Join(failed, "、"))
		}
		return fmt.Errorf("%v（本次补丁已全部回滚）", cause)
	}
	for i, w := range writes {
		if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
			return Result{}, writeErr(fmt.Errorf("创建目录失败（%s）：%v", w.path, err))
		}
		// 快照失败就不写：快照是唯一的后悔药，吞掉它再去写等于裸奔。
		if err := core.SnapshotFile(w.path); err != nil {
			return Result{}, writeErr(fmt.Errorf("写前快照失败（%s）：%v，已中止", w.path, err))
		}
		if err := core.WriteFileAtomic(w.path, encoded[i], 0o644); err != nil {
			core.DiscardLastSnapshot()
			return Result{}, writeErr(fmt.Errorf("写入 %s 失败：%v", filepath.Base(w.path), err))
		}
		done = append(done, w.path)
	}

	return Result{
		Output: fmt.Sprintf("补丁已应用，共写入 %d 个文件：\n- %s\n\n%s",
			len(done), strings.Join(done, "\n- "), summary),
		Detail: clipForTool(summary, 12000),
	}, nil
}

type patchWrite struct {
	path string
	text string
	meta textMeta
}

// parsePatch 把补丁文本解析为按文件分组的改动列表。
func parsePatch(body string) ([]filePatch, error) {
	var out []filePatch
	var cur *filePatch
	var hunk *patchHunk

	flushHunk := func() {
		if cur != nil && hunk != nil && len(hunk.lines) > 0 {
			cur.hunks = append(cur.hunks, *hunk)
		}
		hunk = nil
	}
	var dupErr error
	flushFile := func() {
		flushHunk()
		if cur != nil && len(cur.hunks) > 0 {
			for _, existing := range out {
				if strings.EqualFold(filepath.Clean(existing.path), filepath.Clean(cur.path)) {
					dupErr = fmt.Errorf("同一补丁里重复修改 %s：请合并成一个 *** Update File 段，或拆成多次 apply_patch", cur.path)
					return
				}
			}
			out = append(out, *cur)
		}
		cur = nil
	}

	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for _, raw := range lines {
		line := raw
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "*** Begin Patch" || trimmed == "*** End Patch" || trimmed == "*** End of File" || trimmed == "":
			// 空行在补丁里按"上下文空行"处理，这里直接跳过；
			// 需要精确匹配空行的场景，用单个空格前缀表示。
			continue
		case strings.HasPrefix(trimmed, "*** Update File:") || strings.HasPrefix(trimmed, "*** Update File："):
			flushFile()
			p := strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Update File:"))
			p = strings.TrimSpace(strings.TrimPrefix(p, "*** Update File："))
			if p == "" {
				return nil, fmt.Errorf("*** Update File: 后面缺少文件路径")
			}
			cur = &filePatch{path: p}
		case strings.HasPrefix(trimmed, "*** Add File:") || strings.HasPrefix(trimmed, "*** Add File："):
			flushFile()
			p := strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Add File:"))
			p = strings.TrimSpace(strings.TrimPrefix(p, "*** Add File："))
			if p == "" {
				return nil, fmt.Errorf("*** Add File: 后面缺少文件路径")
			}
			cur = &filePatch{path: p, add: true}
		case strings.HasPrefix(trimmed, "*** Delete File"):
			return nil, fmt.Errorf("apply_patch 不支持删除文件，请改用 delete_path（需要用户确认）")
		case strings.HasPrefix(trimmed, "@@"):
			if cur == nil {
				return nil, fmt.Errorf("@@ 出现在文件声明之前，请先用 *** Update File: 路径 指定文件")
			}
			flushHunk()
			hunk = &patchHunk{at: strings.TrimSpace(strings.TrimPrefix(trimmed, "@@"))}
		default:
			if cur == nil {
				return nil, fmt.Errorf("补丁内容前缺少 *** Update File / *** Add File 声明：%s", strings.TrimSpace(line))
			}
			if hunk == nil {
				hunk = &patchHunk{}
			}
			c := line[0]
			switch c {
			case ' ', '-', '+':
				hunk.lines = append(hunk.lines, line)
			case '\\':
				// "\ No newline at end of file" 之类的注记，忽略
			default:
				return nil, fmt.Errorf("无法识别的补丁行（应以空格/-/+ 开头）：%s", strings.TrimSpace(line))
			}
		}
	}
	flushFile()
	if dupErr != nil {
		return nil, dupErr
	}
	return out, nil
}

// applyHunks 把若干段改动顺序应用到原文，返回新文本。
// 定位采用三级匹配：逐字精确 → 忽略行尾空白 → 忽略首尾空白，
// 这样即便模型抄来的上下文缩进略有出入也能落地，但不做模糊猜测，避免改错位置。
func applyHunks(text string, hunks []patchHunk) (string, error) {
	crlf := strings.Contains(text, "\r\n")
	norm := strings.ReplaceAll(text, "\r\n", "\n")
	endsWithNL := strings.HasSuffix(norm, "\n")
	lines := strings.Split(strings.TrimSuffix(norm, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}

	pos := 0
	for hi, h := range hunks {
		var oldSeq, newSeq []string
		for _, l := range h.lines {
			c := l[0]
			body := l[1:]
			switch c {
			case ' ':
				oldSeq = append(oldSeq, body)
				newSeq = append(newSeq, body)
			case '-':
				oldSeq = append(oldSeq, body)
			case '+':
				newSeq = append(newSeq, body)
			}
		}
		if len(oldSeq) == 0 {
			return "", fmt.Errorf("第 %d 段改动没有上下文或删除行，无法定位", hi+1)
		}

		idx := -1
		for _, mode := range []int{0, 1, 2} {
			idx = findSeq(lines, oldSeq, pos, mode)
			if idx >= 0 {
				break
			}
		}
		if idx < 0 {
			return "", fmt.Errorf("第 %d 段改动未能匹配原文（定位行：%q）。请先用 read_file 核对当前文件内容", hi+1, firstNonEmpty(oldSeq))
		}

		next := make([]string, 0, len(lines)-len(oldSeq)+len(newSeq))
		next = append(next, lines[:idx]...)
		next = append(next, newSeq...)
		next = append(next, lines[idx+len(oldSeq):]...)
		lines = next
		pos = idx + len(newSeq)
	}

	out := strings.Join(lines, "\n")
	if endsWithNL && out != "" {
		out += "\n"
	}
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return out, nil
}

// findSeq 从 from 起查找 seq 出现位置。mode: 0 精确，1 忽略行尾空白，2 忽略首尾空白。
func findSeq(hay, seq []string, from, mode int) int {
	if len(seq) == 0 || len(hay) < len(seq) {
		return -1
	}
	if from < 0 {
		from = 0
	}
	eq := func(a, b string) bool {
		switch mode {
		case 1:
			return strings.TrimRight(a, " \t") == strings.TrimRight(b, " \t")
		case 2:
			return strings.TrimSpace(a) == strings.TrimSpace(b)
		}
		return a == b
	}
	for i := from; i+len(seq) <= len(hay); i++ {
		ok := true
		for j := range seq {
			if !eq(hay[i+j], seq[j]) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func firstNonEmpty(xs []string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// hunkRef 指向"第几个文件的第几段"。
type hunkRef struct {
	file int
	hunk int
}

// hunkCatalog 按文件顺序给所有段落连续编号，并生成人类可读的清单。
func hunkCatalog(patches []filePatch) hunkCatalogT {
	cat := hunkCatalogT{}
	for fi, fp := range patches {
		for hi, h := range fp.hunks {
			cat.refs = append(cat.refs, hunkRef{fi, hi})
			add, del := 0, 0
			for _, l := range h.lines {
				switch {
				case strings.HasPrefix(l, "+"):
					add++
				case strings.HasPrefix(l, "-"):
					del++
				}
			}
			at := strings.TrimSpace(h.at)
			if at == "" {
				at = "（无定位说明）"
			}
			cat.lines = append(cat.lines, fmt.Sprintf("  #%d %s · %s · +%d/-%d",
				len(cat.refs), fp.path, at, add, del))
		}
	}
	return cat
}

type hunkCatalogT struct {
	refs  []hunkRef
	lines []string
}

func (c hunkCatalogT) text() string {
	if len(c.lines) == 0 {
		return "  （无改动段）"
	}
	return strings.Join(c.lines, "\n")
}

// parseHunkSel 解析 "1,3-4" 形式的段落选择，返回 0-based 的保留表。
func parseHunkSel(text string, total int) (map[int]bool, error) {
	keep := map[int]bool{}
	if total == 0 {
		return keep, fmt.Errorf("补丁里没有任何段落可选")
	}
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == '，' || r == ' ' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if lo, hi, ok := splitRange(part); ok {
			if lo < 1 || hi > total || lo > hi {
				return nil, fmt.Errorf("段落范围 %q 超出 1..%d", part, total)
			}
			for i := lo; i <= hi; i++ {
				keep[i-1] = true
			}
			continue
		}
		var n int
		if _, err := fmt.Sscanf(part, "%d", &n); err != nil || n < 1 || n > total {
			return nil, fmt.Errorf("无法识别的段落编号 %q（当前共 %d 段）", part, total)
		}
		keep[n-1] = true
	}
	if len(keep) == 0 {
		return nil, fmt.Errorf("hunks 没有选中任何段落（当前共 %d 段）", total)
	}
	return keep, nil
}

func splitRange(s string) (int, int, bool) {
	i := strings.IndexAny(s, "-~")
	if i <= 0 {
		return 0, 0, false
	}
	var lo, hi int
	if _, err := fmt.Sscanf(strings.TrimSpace(s[:i]), "%d", &lo); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(s[i+1:]), "%d", &hi); err != nil {
		return 0, 0, false
	}
	return lo, hi, true
}

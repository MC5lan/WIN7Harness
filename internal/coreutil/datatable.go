package coreutil

// 结构化数据工具：CSV / TSV / JSON / JSONL 统一成 Table，
// 支持切片、选列、过滤、求和、分组，输出 Markdown 表格。
// 目标是让模型不必写 Python 就能看数据，也不把整张表塞进上下文。

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// tableMaxRows 单表最多读取的数据行数，防止一次把几百 MB CSV 读进内存。
const tableMaxRows = 200000

// Table 一张二维表。所有单元格按字符串保存，数值操作时再解析。
type Table struct {
	Columns []string
	Rows    [][]string
	// Truncated 表示源文件超过 tableMaxRows 被截断。
	Truncated bool
}

// ColIndex 返回列下标，-1 表示不存在。
func (t *Table) ColIndex(name string) int {
	for i, c := range t.Columns {
		if c == name {
			return i
		}
	}
	// 宽松匹配：忽略大小写与空白
	n := strings.ToLower(strings.TrimSpace(name))
	for i, c := range t.Columns {
		if strings.ToLower(strings.TrimSpace(c)) == n {
			return i
		}
	}
	return -1
}

// ParseCSVText 解析 CSV/TSV 文本（自动判断分隔符与 BOM）。
func ParseCSVText(s string) (*Table, error) {
	s = strings.TrimPrefix(s, "\ufeff")
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("内容为空")
	}
	delim := ','
	if strings.Count(firstLine(s), "\t") > strings.Count(firstLine(s), ",") {
		delim = '\t'
	}
	r := csv.NewReader(strings.NewReader(s))
	r.Comma = delim
	r.FieldsPerRecord = -1
	// 流式读：不一次性 ReadAll——否则 500MB CSV 在 32 位进程里直接 OOM，
	// 而且 tableMaxRows 的截断等于没截（在 ReadAll 之后）。
	var recs [][]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV 解析失败：%v", err)
		}
		recs = append(recs, rec)
		if len(recs) >= tableMaxRows+1 {
			break
		}
	}
	if len(recs) == 0 {
		return nil, fmt.Errorf("CSV 没有数据行")
	}
	truncated := false
	if len(recs) > tableMaxRows {
		truncated = true
		recs = recs[:tableMaxRows]
	}
	t := &Table{Columns: trimCells(recs[0]), Truncated: truncated}
	for i, rec := range recs[1:] {
		if i >= tableMaxRows {
			t.Truncated = true
			break
		}
		row := trimCells(rec)
		// 补齐/截断到列数，避免下游越界
		for len(row) < len(t.Columns) {
			row = append(row, "")
		}
		if len(row) > len(t.Columns) {
			row = row[:len(t.Columns)]
		}
		t.Rows = append(t.Rows, row)
	}
	return t, nil
}

// ParseJSONText 解析 JSON 数据：支持对象数组、NDJSON、以及"列名→数组"的对象形式。
func ParseJSONText(data []byte) (*Table, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("内容为空")
	}
	// 先按 NDJSON 尝试（每行一个对象）
	if trimmed[0] == '{' && bytes.Contains(trimmed, []byte("\n{")) {
		if t, err := parseNDJSON(trimmed); err == nil && t != nil {
			return t, nil
		}
	}
	var arr []map[string]interface{}
	if err := json.Unmarshal(trimmed, &arr); err == nil && len(arr) > 0 {
		return tableFromMaps(arr), nil
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(trimmed, &obj); err == nil {
		// 形如 {"name":[...], "age":[...]}
		if t := tableFromColumns(obj); t != nil {
			return t, nil
		}
		// 单个对象当成一行
		return tableFromMaps([]map[string]interface{}{obj}), nil
	}
	return nil, fmt.Errorf("JSON 结构无法识别（需要对象数组、NDJSON 或列数组形式）")
}

func parseNDJSON(data []byte) (*Table, error) {
	var rows []map[string]interface{}
	for _, ln := range bytes.Split(data, []byte("\n")) {
		ln = bytes.TrimSpace(ln)
		if len(ln) == 0 {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal(ln, &m); err != nil {
			return nil, err
		}
		rows = append(rows, m)
		if len(rows) > tableMaxRows {
			break
		}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("没有记录")
	}
	return tableFromMaps(rows), nil
}

func tableFromMaps(rows []map[string]interface{}) *Table {
	// 列顺序：先出现先排，保证输出稳定
	var cols []string
	seen := map[string]bool{}
	for _, r := range rows {
		keys := make([]string, 0, len(r))
		for k := range r {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	t := &Table{Columns: cols}
	for i, r := range rows {
		if i >= tableMaxRows {
			t.Truncated = true
			break
		}
		row := make([]string, len(cols))
		for j, c := range cols {
			row[j] = cellString(r[c])
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

func tableFromColumns(obj map[string]interface{}) *Table {
	var cols []string
	var arrays [][]interface{}
	for k, v := range obj {
		if arr, ok := v.([]interface{}); ok {
			cols = append(cols, k)
			arrays = append(arrays, arr)
		}
	}
	if len(cols) == 0 {
		return nil
	}
	// 稳定列序
	type pair struct {
		name string
		arr  []interface{}
	}
	var ps []pair
	for i, c := range cols {
		ps = append(ps, pair{c, arrays[i]})
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].name < ps[j].name })
	t := &Table{}
	for _, p := range ps {
		t.Columns = append(t.Columns, p.name)
	}
	maxLen := 0
	for _, p := range ps {
		if len(p.arr) > maxLen {
			maxLen = len(p.arr)
		}
	}
	for r := 0; r < maxLen; r++ {
		row := make([]string, len(ps))
		for c, p := range ps {
			if r < len(p.arr) {
				row[c] = cellString(p.arr[r])
			}
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

func cellString(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// LoadTable 按扩展名读取表数据。
// LoadTable 读取 CSV/JSON 文件为 Table。
// CSV 走流式解析：不整个读进内存，超 tableMaxRows 截断（Truncated=true）；
// JSON 必须完整解析，单独限制 512MB。
func LoadTable(path string) (*Table, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json", ".jsonl", ".ndjson":
		const maxJSON = 512 << 20
		if st.Size() > maxJSON {
			return nil, fmt.Errorf("JSON 文件过大（%s），拒绝载入", HumanSize(st.Size()))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return ParseJSONText(data)
	default:
		return parseCSVFile(path)
	}
}

// parseCSVFile 流式解析 CSV/TSV 文件：不把整个文件读进内存，
// 超 tableMaxRows 行截断（Truncated=true）。
func parseCSVFile(path string) (*Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// 读第一行做分隔符嗅探
	br := bufio.NewReader(f)
	first, _, _ := br.ReadLine()
	delim := ','
	if bytes.Count(first, []byte{'\t'}) > bytes.Count(first, []byte{','}) {
		delim = '\t'
	}
	// 复位到文件开头
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	// 跳过 UTF-8 BOM（如果有）
	peek := make([]byte, 3)
	if n, err := io.ReadFull(f, peek); err == nil {
		if !(n == 3 && peek[0] == 0xEF && peek[1] == 0xBB && peek[2] == 0xBF) {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return nil, err
			}
		}
	}
	r := csv.NewReader(f)
	r.Comma = delim
	r.FieldsPerRecord = -1
	var recs [][]string
	truncated := false
	for len(recs) <= tableMaxRows {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV 解析失败：%v", err)
		}
		recs = append(recs, rec)
	}
	if len(recs) > tableMaxRows {
		truncated = true
		recs = recs[:tableMaxRows]
	}
	if len(recs) == 0 {
		return nil, fmt.Errorf("CSV 没有数据行")
	}
	t := &Table{Columns: trimCells(recs[0]), Truncated: truncated}
	for _, rec := range recs[1:] {
		row := trimCells(rec)
		for len(row) < len(t.Columns) {
			row = append(row, "")
		}
		if len(row) > len(t.Columns) {
			row = row[:len(t.Columns)]
		}
		t.Rows = append(t.Rows, row)
	}
	return t, nil
}

// Slice 取一段行（offset 从 0 开始）。
func (t *Table) Slice(offset, limit int) *Table {
	if offset < 0 {
		offset = 0
	}
	if offset > len(t.Rows) {
		offset = len(t.Rows)
	}
	end := len(t.Rows)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return &Table{Columns: append([]string{}, t.Columns...), Rows: t.Rows[offset:end]}
}

// Select 只保留指定列。
func (t *Table) Select(cols []string) (*Table, error) {
	var idx []int
	for _, c := range cols {
		i := t.ColIndex(c)
		if i < 0 {
			return nil, fmt.Errorf("没有列 %q，可用列：%s", c, strings.Join(t.Columns, ", "))
		}
		idx = append(idx, i)
	}
	out := &Table{Columns: append([]string{}, cols...)}
	for _, r := range t.Rows {
		row := make([]string, len(idx))
		for k, i := range idx {
			row[k] = r[i]
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// Filter 按列过滤。op 支持 = != > >= < <= contains startswith empty notempty。
func (t *Table) Filter(col, op, val string) (*Table, error) {
	i := t.ColIndex(col)
	if i < 0 {
		return nil, fmt.Errorf("没有列 %q，可用列：%s", col, strings.Join(t.Columns, ", "))
	}
	op = strings.ToLower(strings.TrimSpace(op))
	if op == "" {
		op = "="
	}
	out := &Table{Columns: append([]string{}, t.Columns...)}
	for _, r := range t.Rows {
		cell := r[i]
		ok := false
		switch op {
		case "=", "==":
			ok = cell == val || strings.EqualFold(cell, val)
		case "!=", "<>":
			ok = cell != val && !strings.EqualFold(cell, val)
		case "contains":
			ok = strings.Contains(strings.ToLower(cell), strings.ToLower(val))
		case "startswith":
			ok = strings.HasPrefix(strings.ToLower(cell), strings.ToLower(val))
		case "empty":
			ok = strings.TrimSpace(cell) == ""
		case "notempty":
			ok = strings.TrimSpace(cell) != ""
		case ">", ">=", "<", "<=":
			a, err1 := strconv.ParseFloat(cell, 64)
			b, err2 := strconv.ParseFloat(val, 64)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("列 %q 不是数值，无法做 %s 比较", col, op)
			}
			switch op {
			case ">":
				ok = a > b
			case ">=":
				ok = a >= b
			case "<":
				ok = a < b
			case "<=":
				ok = a <= b
			}
		default:
			return nil, fmt.Errorf("不支持的比较符 %q（可用 = != > >= < <= contains startswith empty notempty）", op)
		}
		if ok {
			out.Rows = append(out.Rows, r)
		}
	}
	return out, nil
}

// Sum 对数值列求和，返回和、参与计算的行数。
func (t *Table) Sum(col string) (float64, int, error) {
	i := t.ColIndex(col)
	if i < 0 {
		return 0, 0, fmt.Errorf("没有列 %q，可用列：%s", col, strings.Join(t.Columns, ", "))
	}
	var sum float64
	var n int
	for _, r := range t.Rows {
		s := strings.TrimSpace(r[i])
		if s == "" {
			continue
		}
		v, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
		if err != nil {
			continue
		}
		sum += v
		n++
	}
	return sum, n, nil
}

// KV 分组计数项。
type KV struct {
	Key   string
	Count int
}

// GroupCount 按列分组计数，按次数倒序，取 top 个。
func (t *Table) GroupCount(col string, top int) ([]KV, error) {
	i := t.ColIndex(col)
	if i < 0 {
		return nil, fmt.Errorf("没有列 %q，可用列：%s", col, strings.Join(t.Columns, ", "))
	}
	m := map[string]int{}
	var order []string
	for _, r := range t.Rows {
		k := r[i]
		if _, ok := m[k]; !ok {
			order = append(order, k)
		}
		m[k]++
	}
	out := make([]KV, 0, len(m))
	for _, k := range order {
		out = append(out, KV{Key: k, Count: m[k]})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Count > out[b].Count })
	if top > 0 && len(out) > top {
		out = out[:top]
	}
	return out, nil
}

// Markdown 渲染表格，maxRows<=0 时最多 50 行。
func (t *Table) Markdown(maxRows int) string {
	if maxRows <= 0 {
		maxRows = 50
	}
	maxCell := 40
	var b strings.Builder
	b.WriteString("| " + strings.Join(t.Columns, " | ") + " |\n")
	b.WriteString("|")
	for range t.Columns {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for i, r := range t.Rows {
		if i >= maxRows {
			fmt.Fprintf(&b, "\n…共 %d 行，已省略 %d 行\n", len(t.Rows), len(t.Rows)-maxRows)
			break
		}
		cells := make([]string, len(r))
		for j, c := range r {
			c = strings.ReplaceAll(c, "|", "\\|")
			c = strings.ReplaceAll(c, "\n", " ")
			if len([]rune(c)) > maxCell {
				c = string([]rune(c)[:maxCell]) + "…"
			}
			cells[j] = c
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func trimCells(in []string) []string {
	out := make([]string, len(in))
	for i, c := range in {
		out[i] = strings.TrimSpace(strings.TrimPrefix(c, "\ufeff"))
	}
	return out
}

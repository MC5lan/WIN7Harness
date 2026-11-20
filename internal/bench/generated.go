package bench

import "fmt"

// generatedTasks 是不依赖 testdata 目录、直接用内存 fixture 生成的工程任务。
// 目标是让 bench 覆盖：Unicode、数值、边界、解析、跨文件、错误链、路径、
// 排序稳定性、上下文取消、CSV、缓存过期等常见工程缺陷。
func generatedTasks() []Task {
	common := "你在一个临时 Go 模块里修一个失败测试。先运行 `go test ./...` 看失败原因；" +
		"只改非测试源文件，不要修改任何 *_test.go、go.mod 或 go.sum；" +
		"改完再运行 `go test ./...` 确认通过。最后用一句话说明改了什么。"

	mk := func(id, name, job string, files map[string]string) Task {
		files["go.mod"] = "module " + id + "\n\ngo 1.20\n"
		return Task{ID: id, Name: name, Prompt: common + "\n\n任务：" + job, Files: files}
	}

	return []Task{
		mk("runerev", "Unicode 反转", "让 Reverse 按 Unicode 字符反转，而不是按字节。", map[string]string{
			"rev.go": `package runerev

func Reverse(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}
`,
			"rev_test.go": `package runerev

import "testing"

func TestReverseUnicode(t *testing.T) {
	if got := Reverse("你好ab"); got != "ba好你" {
		t.Fatalf("Reverse = %q", got)
	}
}
`,
		}),

		mk("avgfloat", "平均值精度", "让 Mean 返回正确的浮点平均值，不要先做整数除法。", map[string]string{
			"mean.go": `package avgfloat

func Mean(xs []int) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return float64(sum / len(xs))
}
`,
			"mean_test.go": `package avgfloat

import "testing"

func TestMean(t *testing.T) {
	if got := Mean([]int{1, 2}); got != 1.5 {
		t.Fatalf("Mean = %v", got)
	}
}
`,
		}),

		mk("clamp", "区间夹取", "让 Clamp 同时处理下界和上界。", map[string]string{
			"clamp.go": `package clamp

func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	return v
}
`,
			"clamp_test.go": `package clamp

import "testing"

func TestClampHigh(t *testing.T) {
	if got := Clamp(10, 1, 5); got != 5 {
		t.Fatalf("Clamp = %d", got)
	}
}
`,
		}),

		mk("parsebool", "布尔解析", "让 Parse 接受 yes/on/no/off（大小写不敏感），非法值返回错误。", map[string]string{
			"parse.go": `package parsebool

import (
	"errors"
	"strings"
)

func Parse(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	}
	return false, errors.New("invalid bool")
}
`,
			"parse_test.go": `package parsebool

import "testing"

func TestParseWords(t *testing.T) {
	for _, s := range []string{"yes", "on"} {
		if v, err := Parse(s); err != nil || !v {
			t.Fatalf("Parse(%q) = %v, %v", s, v, err)
		}
	}
	for _, s := range []string{"no", "off"} {
		if v, err := Parse(s); err != nil || v {
			t.Fatalf("Parse(%q) = %v, %v", s, v, err)
		}
	}
}
`,
		}),

		mk("mapmerge", "配置合并不污染入参", "让 Merge 返回新 map，不修改 base；override 的同名键覆盖 base。", map[string]string{
			"config.go": `package mapmerge

type Config struct {
	Values map[string]string
}

func Merge(base, override Config) Config {
	for k, v := range override.Values {
		base.Values[k] = v
	}
	return base
}
`,
			"config_test.go": `package mapmerge

import "testing"

func TestMergeDoesNotMutateBase(t *testing.T) {
	base := Config{Values: map[string]string{"a": "1"}}
	out := Merge(base, Config{Values: map[string]string{"a": "2", "b": "3"}})
	if base.Values["a"] != "1" {
		t.Fatalf("base 被修改: %v", base.Values)
	}
	if out.Values["a"] != "2" || out.Values["b"] != "3" {
		t.Fatalf("merge 结果错误: %v", out.Values)
	}
}
`,
		}),

		mk("svcmiss", "服务层错误传播", "让 Lookup 在 Store 找不到 id 时返回错误，不能静默返回空串。", map[string]string{
			"store/store.go": `package store

type Store interface {
	Get(id string) (string, bool)
}

type Memory map[string]string

func (m Memory) Get(id string) (string, bool) {
	v, ok := m[id]
	return v, ok
}
`,
			"service/service.go": `package service

import "svcmiss/store"

func Lookup(s store.Store, id string) (string, error) {
	v, _ := s.Get(id)
	return v, nil
}
`,
			"service/service_test.go": `package service

import (
	"testing"

	"svcmiss/store"
)

func TestLookupMissing(t *testing.T) {
	if _, err := Lookup(store.Memory{}, "nope"); err == nil {
		t.Fatal("missing id 应返回错误")
	}
}
`,
		}),

		mk("http404", "HTTP 未命中返回 404", "让 Handler 在 id 不存在时返回 404，不要返回 200 + 空用户。", map[string]string{
			"handler.go": `package http404

import (
	"encoding/json"
	"net/http"
)

var db = map[string]string{"1": "alice"}

func Handler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	name := db[id]
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"name": name})
}
`,
			"handler_test.go": `package http404

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMissingUser404(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/user?id=404", nil)
	rec := httptest.NewRecorder()
	Handler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}
`,
		}),

		mk("paging", "分页偏移", "让 Offset 使用 1-based 页码：第 1 页偏移 0，第 3 页偏移 20（size=10）。", map[string]string{
			"page.go": `package paging

func Offset(page, size int) int {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	return page * size
}
`,
			"page_test.go": `package paging

import "testing"

func TestOffset(t *testing.T) {
	if got := Offset(1, 10); got != 0 {
		t.Fatalf("page1 = %d", got)
	}
	if got := Offset(3, 10); got != 20 {
		t.Fatalf("page3 = %d", got)
	}
}
`,
		}),

		mk("retrymax", "重试次数", "让 MaxAttempts(3) 返回 3；n<=0 时返回 0。", map[string]string{
			"retry.go": `package retrymax

func MaxAttempts(n int) int {
	if n <= 0 {
		return 0
	}
	return n - 1
}
`,
			"retry_test.go": `package retrymax

import "testing"

func TestMaxAttempts(t *testing.T) {
	if got := MaxAttempts(3); got != 3 {
		t.Fatalf("MaxAttempts(3) = %d", got)
	}
}
`,
		}),

		mk("ttlcache", "缓存过期", "让 Get 在 now 不早于 Expires 时返回未命中。", map[string]string{
			"cache.go": `package ttlcache

import "time"

type Entry struct {
	Value   string
	Expires time.Time
}

type Cache struct {
	m map[string]Entry
}

func New() *Cache { return &Cache{m: map[string]Entry{}} }

func (c *Cache) Set(k, v string, exp time.Time) {
	c.m[k] = Entry{Value: v, Expires: exp}
}

func (c *Cache) Get(k string, now time.Time) (string, bool) {
	e, ok := c.m[k]
	if !ok {
		return "", false
	}
	return e.Value, true
}
`,
			"cache_test.go": `package ttlcache

import (
	"testing"
	"time"
)

func TestExpiredMiss(t *testing.T) {
	c := New()
	c.Set("a", "v", time.Unix(10, 0))
	if _, ok := c.Get("a", time.Unix(10, 0)); ok {
		t.Fatal("到期时应未命中")
	}
}
`,
		}),

		mk("errwrap", "错误链可判断", "让 Wrap 使用 %w 包装 ErrNotFound，使 errors.Is 成立。", map[string]string{
			"errs.go": `package errwrap

import (
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("not found")

func Wrap() error {
	return fmt.Errorf("load: %v", ErrNotFound)
}
`,
			"errs_test.go": `package errwrap

import (
	"errors"
	"testing"
)

func TestErrorsIs(t *testing.T) {
	if !errors.Is(Wrap(), ErrNotFound) {
		t.Fatal("errors.Is 应命中 ErrNotFound")
	}
}
`,
		}),

		mk("pathjoin", "Windows 路径拼接", "让 Join 使用平台路径分隔符并清理多余分隔符，与 filepath.Join 一致。", map[string]string{
			"path.go": `package pathjoin

import "strings"

func Join(parts ...string) string {
	return strings.Join(parts, "/")
}
`,
			"path_test.go": `package pathjoin

import (
	"path/filepath"
	"testing"
)

func TestJoinMatchesFilepath(t *testing.T) {
	got := Join("a", "b", "c.txt")
	want := filepath.Join("a", "b", "c.txt")
	if got != want {
		t.Fatalf("Join = %q, want %q", got, want)
	}
}
`,
		}),

		mk("stablesort", "稳定排序", "让 Sort 在 Key 相同时保持原始顺序，使用稳定排序。", map[string]string{
			"sort.go": `package stablesort

import "sort"

type Item struct {
	Key string
	N   int
}

func Sort(items []Item) {
	sort.Slice(items, func(i, j int) bool {
		return items[i].Key < items[j].Key
	})
}
`,
			"sort_test.go": `package stablesort

import "testing"

func TestStable(t *testing.T) {
	items := make([]Item, 1000)
	var want []int
	for i := range items {
		key := "a"
		if i%2 == 1 {
			key = "b"
		}
		items[i] = Item{key, i}
	}
	// a 组（偶数）按原始顺序在前，b 组（奇数）按原始顺序在后。
	for i := 0; i < len(items); i += 2 {
		want = append(want, i)
	}
	for i := 1; i < len(items); i += 2 {
		want = append(want, i)
	}
	Sort(items)
	for i, w := range want {
		if items[i].N != w {
			t.Fatalf("不稳定: 第 %d 个 = %d, want %d", i, items[i].N, w)
		}
	}
}
`,
		}),

		mk("cancel", "上下文取消", "让 Run 在 ctx 取消后停止，不再继续调用 fn。", map[string]string{
			"worker.go": `package cancel

import "context"

func Run(ctx context.Context, n int, fn func(int)) {
	for i := 0; i < n; i++ {
		fn(i)
	}
}
`,
			"worker_test.go": `package cancel

import (
	"context"
	"testing"
)

func TestCancel(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	seen := 0
	Run(ctx, 10, func(i int) {
		seen++
		if seen == 2 {
			stop()
		}
	})
	if seen != 2 {
		t.Fatalf("seen = %d", seen)
	}
}
`,
		}),

		mk("csvparse", "CSV 引号", "让 Parse 正确处理带引号字段里的逗号，例如 a,\"b,c\",d。", map[string]string{
			"parse.go": `package csvparse

import "strings"

func Parse(s string) []string {
	return strings.Split(s, ",")
}
`,
			"parse_test.go": `package csvparse

import (
	"reflect"
	"testing"
)

func TestQuotedComma(t *testing.T) {
	got := Parse("a,\"b,c\",d")
	want := []string{"a", "b,c", "d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}
`,
		}),

		mk("longhorizon", "10 包长链路修复", "p01~p10 十个包的 Value 都应返回自己的编号；逐个修好，不要改测试。", longHorizonFiles()),

		mk("refactor", "10 文件重命名重构", "把 calc 包的 Old 重命名为 Compute，并更新 use01~use10 的全部调用点；不要改测试。", refactorFiles()),
	}
}

func longHorizonFiles() map[string]string {
	files := map[string]string{}
	for i := 1; i <= 10; i++ {
		n := fmt.Sprintf("%02d", i)
		pkg := "p" + n
		files[pkg+"/"+pkg+".go"] = fmt.Sprintf("package %s\n\nfunc Value() int { return 0 }\n", pkg)
		files[pkg+"/"+pkg+"_test.go"] = fmt.Sprintf(`package %s

import "testing"

func TestValue(t *testing.T) {
	if Value() != %d {
		t.Fatalf("Value = %%d", Value())
	}
}
`, pkg, i)
	}
	return files
}

func refactorFiles() map[string]string {
	files := map[string]string{
		"calc/calc.go": "package calc\n\nfunc Old(x int) int { return x * 2 }\n",
	}
	for i := 1; i <= 10; i++ {
		files[fmt.Sprintf("calc/use%02d.go", i)] = fmt.Sprintf("package calc\n\nfunc F%02d() int { return Old(%d) }\n", i, i)
	}
	var calls string
	for i := 1; i <= 10; i++ {
		calls += fmt.Sprintf("\tif F%02d() != %d {\n\t\tt.Fatalf(\"F%02d = %%d\", F%02d())\n\t}\n", i, i*2, i, i)
	}
	files["calc/refactor_test.go"] = `package calc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

func TestCompute(t *testing.T) {
	if Compute(2) != 4 {
		t.Fatalf("Compute(2) = %d", Compute(2))
	}
` + calls + `
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, f := range files {
		if filepath.Base(f) == "refactor_test.go" {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "Old" {
				t.Fatalf("Old 仍然存在: %s", f)
			}
		}
	}
}
`
	return files
}

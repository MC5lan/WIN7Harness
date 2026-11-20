package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 这些用例都拿真实样本走主路径：解析器吃的是引擎真实返回过的 HTML 片段，
// 历史检索真的落盘几个会话文件再查，诊断解析吃的是编译器真实输出。
// 只断言"函数存在"没有意义，线上炸的永远是边界。

// testConv / testMsg 只保留检索用得到的字段，结构标签与线上一致。
type testConv struct {
	ID    string    `json:"id"`
	Title string    `json:"title"`
	At    time.Time `json:"at"`
	Msgs  []testMsg `json:"msgs"`
}

type testMsg struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

const ddgSample = `<div class="result results_links results_links_deep web-result ">
<div class="links_main links_deep result__body">
<h2 class="result__title">
<a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F&amp;rut=abc">The Go Programming Language</a>
</h2>
<a class="result__snippet" href="#">Go is an open source programming language that makes it easy to build simple, reliable, and efficient software.</a>
</div>
</div>
<div class="result results_links">
<div class="links_main result__body">
<h2 class="result__title"><a class="result__a" href="https://pkg.go.dev/net/http">net/http package</a></h2>
<a class="result__snippet">Package http provides HTTP client and server implementations.</a>
</div>
</div>`

const bingSample = `<li class="b_algo"><h2><a href="https://example.com/a">Example A</a></h2>
<p>First snippet about A.</p></li>
<li class="b_algo"><h2><a href="https://example.com/b">Example B</a></h2>
<p>Second snippet about B.</p></li>`

// ST33：搜索结果解析要能剥掉跳转壳、还原真实地址，并且按 limit 截断。
func TestParseSearchPageV124(t *testing.T) {
	hits := ParseSearchPageForTest("duckduckgo", ddgSample, 10)
	if len(hits) != 2 {
		t.Fatalf("DDG 解析条数 = %d, 期望 2", len(hits))
	}
	if hits[0].URL != "https://go.dev/doc/" {
		t.Fatalf("DDG 第一条真实地址 = %q, 期望剥掉 uddg 跳转壳", hits[0].URL)
	}
	if !strings.Contains(hits[0].Title, "Go Programming") {
		t.Fatalf("DDG 标题解析异常: %q", hits[0].Title)
	}
	if !strings.Contains(hits[1].Snippet, "HTTP client") {
		t.Fatalf("DDG 摘要解析异常: %q", hits[1].Snippet)
	}
	if got := ParseSearchPageForTest("duckduckgo", ddgSample, 1); len(got) != 1 {
		t.Fatalf("limit=1 应只返回 1 条，实际 %d", len(got))
	}

	bing := ParseSearchPageForTest("bing", bingSample, 10)
	if len(bing) != 2 {
		t.Fatalf("Bing 解析条数 = %d, 期望 2", len(bing))
	}
	if bing[0].URL != "https://example.com/a" {
		t.Fatalf("Bing 地址解析异常: %q", bing[0].URL)
	}

	// 空页与噪声页必须安全返回 0 条，不能 panic 也不能伪造结果。
	for _, engine := range WebSearchEngines() {
		if got := ParseSearchPageForTest(engine, "", 5); len(got) != 0 {
			t.Fatalf("%s 空页应返回 0 条，实际 %d", engine, len(got))
		}
		if got := ParseSearchPageForTest(engine, "<html><body>no results</body></html>", 5); len(got) != 0 {
			t.Fatalf("%s 无结果页应返回 0 条，实际 %d", engine, len(got))
		}
	}
}

// writeSession 按 app 层真实落盘格式（外层 {"conv":{...}}）写一个会话文件，
// 检索侧读的是线上同一种结构，测试才不会自证清白。
func writeSession(t *testing.T, dir, name string, conv testConv) string {
	t.Helper()
	b, err := json.Marshal(map[string]interface{}{"v": 1, "conv": conv})
	if err != nil {
		t.Fatalf("序列化会话失败: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatalf("写会话文件失败: %v", err)
	}
	return p
}

// ST34：跨会话检索要真的从落盘文件里捞出命中，并且支持多词 AND 与角色标注。
func TestSearchHistoryV124(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.AddDate(0, 0, -40)

	writeSession(t, dir, "s1.json", testConv{
		ID: "s1", Title: "索引内存优化", At: now,
		Msgs: []testMsg{
			{Role: "user", Text: "索引构建时内存峰值太高了，怎么办", At: now.Add(-3 * time.Minute)},
			{Role: "assistant", Text: "改成整份快照复用 prev.LastBuild.MemBytes 即可", At: now.Add(-2 * time.Minute)},
		},
	})
	writeSession(t, dir, "s2.json", testConv{
		ID: "s2", Title: "无关会话", At: old,
		Msgs: []testMsg{
			{Role: "user", Text: "帮我写个 Python 脚本", At: old},
		},
	})

	hits, scanned, err := SearchHistory(dir, "内存峰值", 10, 0, now)
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if scanned != 2 {
		t.Fatalf("扫描会话数 = %d, 期望 2", scanned)
	}
	if len(hits) != 1 {
		t.Fatalf("命中数 = %d, 期望 1: %+v", len(hits), hits)
	}
	if hits[0].Role != "user" || !strings.Contains(hits[0].Around, "内存峰值") {
		t.Fatalf("命中角色/上下文异常: %+v", hits[0])
	}
	if hits[0].Title != "索引内存优化" {
		t.Fatalf("命中标题 = %q", hits[0].Title)
	}

	// 多词 AND：两个词缺一不可。
	if got, _, err := SearchHistory(dir, "内存峰值 Python", 10, 0, now); err != nil || len(got) != 0 {
		t.Fatalf("多词 AND 应无命中，实际 %d 条 (err=%v)", len(got), err)
	}
	// 单条消息内必须同时含全部词：user 消息只说"内存峰值"、assistant 消息才说"复用"，
	// 因此 "内存 复用" 在单条消息粒度上无命中，这是 AND 语义的正确结果。
	if got, _, _ := SearchHistory(dir, "内存 复用", 10, 0, now); len(got) != 0 {
		t.Fatalf("跨消息不应算 AND 命中，实际 %d 条", len(got))
	}

	// 时间窗过滤：只查最近 7 天应排除 40 天前的会话。
	if _, scanned7, _ := SearchHistory(dir, "Python", 10, 7, now); scanned7 != 1 {
		t.Fatalf("7 天窗口内扫描数 = %d, 期望 1", scanned7)
	}

	// 目录不存在时不能崩，返回空即可。
	if got, _, err := SearchHistory(filepath.Join(dir, "nope"), "内存", 10, 0, now); err != nil || len(got) != 0 {
		t.Fatalf("空目录应返回空且不报错，实际 %d 条 (err=%v)", len(got), err)
	}
}

// ST35：诊断解析要能吃到 Go 编译错误、测试失败与 Python 回溯，并抽出行号。
func TestParseDiagnosticsV124(t *testing.T) {
	goErr := `# win7harness/internal/app
internal/app/chatview.go:332:8: t.Expanded undefined (type **ToolCard has no field or method Expanded)
internal/app/app.go:1511:30: too many arguments in call to a.drawToolGroup
FAIL	win7harness/internal/app [build failed]`
	ds := ParseDiagnostics(goErr, "")
	if len(ds) < 2 {
		t.Fatalf("Go 编译错误应抽出 ≥2 条，实际 %d: %+v", len(ds), ds)
	}
	if ds[0].File != "internal/app/chatview.go" || ds[0].Line != 332 || ds[0].Col != 8 {
		t.Fatalf("首条定位 = %s:%d:%d", ds[0].File, ds[0].Line, ds[0].Col)
	}
	if !strings.Contains(ds[0].Msg, "undefined") {
		t.Fatalf("首条消息 = %q", ds[0].Msg)
	}
	block := FormatDiagnostics(ds)
	if !strings.Contains(block, "chatview.go:332") {
		t.Fatalf("格式化后应保留 file:line，实际:\n%s", block)
	}

	// Python 侧两条通路都要能用：pytest 的 path:line: msg 与 traceback 的 File "x.py", line N
	pyShort := `app/main.py:42: ValueError: bad value`
	if pds := ParseDiagnostics(pyShort, "pytest"); len(pds) == 0 || pds[0].Line != 42 {
		t.Fatalf("Python path:line 解析异常: %+v", pds)
	}
	pyTrace := "Traceback (most recent call last):\n  File \"app/main.py\", line 42, in <module>\n    run()\nValueError: bad value"
	if pds := ParseDiagnostics(pyTrace, ""); len(pds) == 0 || pds[0].Line != 42 || !strings.Contains(pds[0].File, "main.py") {
		t.Fatalf("Python traceback 解析异常: %+v", pds)
	}

	// 没有错误的输出不该产出诊断，避免把正常日志误判成报错。
	if got := ParseDiagnostics("ok\nall tests passed\nPASS", ""); len(got) != 0 {
		t.Fatalf("正常输出不应产生诊断，实际 %+v", got)
	}
	if block := FormatDiagnostics(nil); block != "" {
		t.Fatalf("空诊断应返回空串，实际 %q", block)
	}
}

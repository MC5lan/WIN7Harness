package core

import (
	"strings"
	"testing"
)

func TestParseScriptBasic(t *testing.T) {
	text := "# 每日整理\r\n\r\n整理 D:/待办 里的文件\r\n// 第二行注释\r\n写一份今日总结\n"
	tasks, err := ParseScript(text, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("应解析出 2 条任务，实际 %d: %+v", len(tasks), tasks)
	}
	if tasks[0].Text != "整理 D:/待办 里的文件" || tasks[0].Line != 3 || tasks[0].No != 1 {
		t.Fatalf("第一条不对: %+v", tasks[0])
	}
	if tasks[1].No != 2 || tasks[1].Line != 5 {
		t.Fatalf("第二条不对: %+v", tasks[1])
	}
}

func TestExpandVars(t *testing.T) {
	vars := ScriptVars(`D:\work`, "2026-11-14", "09:30")
	got := ExpandVars("把 {{dir}} 里 {{date}} 的文件整理好，{{datetime}} 前完成", vars)
	want := `把 D:\work 里 2026-11-14 的文件整理好，2026-11-14 09:30 前完成`
	if got != want {
		t.Fatalf("替换结果不对:\n got=%s\nwant=%s", got, want)
	}
	// 未知占位符保持原样，避免用户自定义模板被吃掉
	if out := ExpandVars("读 {{path}}", vars); out != "读 {{path}}" {
		t.Fatalf("未知占位符不该被替换: %s", out)
	}
}

func TestDecodeScriptEncodings(t *testing.T) {
	// UTF-16 LE 带 BOM（Win7 记事本"Unicode"）
	src := "\uFEFF第一行\r\n第二行"
	le := []byte{0xFF, 0xFE}
	for _, r := range src {
		le = append(le, byte(r), byte(r>>8))
	}
	out, err := DecodeScript(le)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "第一行") || !strings.Contains(out, "第二行") {
		t.Fatalf("UTF-16 解码失败: %q", out)
	}

	// UTF-8 BOM
	out, err = DecodeScript(append([]byte{0xEF, 0xBB, 0xBF}, []byte("干活")...))
	if err != nil || out != "干活" {
		t.Fatalf("UTF-8 BOM 处理失败: %q %v", out, err)
	}

	// GBK 应明确报错，而不是拿乱码去执行
	if _, err := DecodeScript([]byte{0xB4, 0xF2, 0xBF, 0xAA}); err == nil {
		t.Fatal("非 UTF-8 应报错")
	}
}

func TestSummarizeScript(t *testing.T) {
	// 全成功
	ok, fail, skip, code := SummarizeScript(3, []TaskStatus{{OK: true}, {OK: true}, {OK: true}})
	if ok != 3 || fail != 0 || skip != 0 || code != 0 {
		t.Fatalf("全成功: %d %d %d %d", ok, fail, skip, code)
	}
	// 第 2 条失败即停，后面 2 条没跑
	ok, fail, skip, code = SummarizeScript(4, []TaskStatus{{OK: true}, {OK: false}})
	if ok != 1 || fail != 1 || skip != 2 || code != 1 {
		t.Fatalf("中途停下: %d %d %d %d", ok, fail, skip, code)
	}
	// 超时优先于失败
	_, _, _, code = SummarizeScript(2, []TaskStatus{{OK: false, TimedOut: true}})
	if code != 3 {
		t.Fatalf("超时应返回 3，实际 %d", code)
	}
	// --keep-going 跑完仍有失败
	_, _, _, code = SummarizeScript(2, []TaskStatus{{OK: true}, {OK: false}, {OK: true}})
	if code != 1 {
		t.Fatalf("有失败应返回 1，实际 %d", code)
	}
}

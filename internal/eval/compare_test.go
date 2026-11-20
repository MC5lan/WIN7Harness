package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func report(results ...Result) Report {
	r := Report{Results: results}
	for _, x := range results {
		switch x.Status {
		case StatusPass:
			r.Pass++
		case StatusFail:
			r.Fail++
		default:
			r.Skip++
		}
	}
	return r
}

func TestCompareNoRegression(t *testing.T) {
	base := report(
		Result{ID: "B01", Status: StatusPass},
		Result{ID: "B02", Status: StatusFail},
	)
	cur := report(
		Result{ID: "B01", Status: StatusPass},
		Result{ID: "B02", Status: StatusPass},
	)
	c := Compare(base, cur)
	if !c.OK() {
		t.Fatalf("不应有退步：%+v", c)
	}
	if len(c.Improvements) != 1 || c.Improvements[0] != "B02" {
		t.Fatalf("应记录 B02 改善：%+v", c.Improvements)
	}
	if c.CurrentScore != 1 {
		t.Fatalf("当前通过率应为 1，实得 %v", c.CurrentScore)
	}
}

func TestCompareRegressionAndSkip(t *testing.T) {
	base := report(
		Result{ID: "B01", Status: StatusPass},
		Result{ID: "B02", Status: StatusPass},
		Result{ID: "B03", Status: StatusPass},
	)
	cur := report(
		Result{ID: "B01", Status: StatusFail},
		Result{ID: "B02", Status: StatusSkip},
		Result{ID: "B03", Status: StatusPass},
	)
	c := Compare(base, cur)
	if c.OK() {
		t.Fatal("失败和跳过都应算退步")
	}
	if len(c.Regressions) != 2 {
		t.Fatalf("应有 2 条退步，实得 %+v", c.Regressions)
	}
}

func TestCompareAddedAndMissing(t *testing.T) {
	base := report(Result{ID: "B01", Status: StatusPass})
	cur := report(Result{ID: "B02", Status: StatusPass})
	c := Compare(base, cur)
	if c.OK() {
		t.Fatal("用例消失必须判失败")
	}
	if len(c.Missing) != 1 || c.Missing[0] != "B01" {
		t.Fatalf("缺失用例不对：%+v", c.Missing)
	}
	if len(c.Added) != 1 || c.Added[0] != "B02" {
		t.Fatalf("新增用例不对：%+v", c.Added)
	}
}

func TestLoadReportAndMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "base.json")
	base := report(Result{ID: "B01", Status: StatusPass})
	b, _ := json.Marshal(base)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Results) != 1 || got.Results[0].ID != "B01" {
		t.Fatalf("读取基线不对：%+v", got)
	}
	md := Compare(base, report(Result{ID: "B01", Status: StatusFail})).Markdown()
	for _, want := range []string{"有退步", "B01"} {
		if !strings.Contains(md, want) {
			t.Fatalf("对比报告缺少 %q：\n%s", want, md)
		}
	}
}

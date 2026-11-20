package eval

import (
	"path/filepath"
	"testing"
)

func TestCasesComplete(t *testing.T) {
	cs := Cases()
	if len(cs) < 9 {
		t.Fatalf("用例数 %d 少于 9", len(cs))
	}
	seen := map[string]bool{}
	for _, c := range cs {
		if c.ID == "" || c.Name == "" || c.Fn == nil {
			t.Fatalf("用例字段缺失：%+v", c)
		}
		if seen[c.ID] {
			t.Fatalf("用例编号重复：%s", c.ID)
		}
		seen[c.ID] = true
	}
}

func TestRunAllBasePass(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cases")
	rep := Run(root, false)
	if rep.Fail > 0 {
		for _, r := range rep.Results {
			if r.Status == StatusFail {
				t.Errorf("%s 失败：%s", r.ID, r.Detail)
			}
		}
	}
	if rep.Pass == 0 {
		t.Fatal("没有任何用例通过")
	}
	if s := rep.Score(); s < 0.99 {
		t.Fatalf("通过率偏低：%.2f", s)
	}
	if rep.Markdown() == "" {
		t.Fatal("报告为空")
	}
}

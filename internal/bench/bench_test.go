package bench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func taskByID(t *testing.T, id string) Task {
	t.Helper()
	for _, task := range Tasks() {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("找不到任务 %s", id)
	return Task{}
}

func TestRunArgsFix(t *testing.T) {
	task := taskByID(t, "argsfix")
	agent := func(_ context.Context, dir, _ string) (string, int, error) {
		path := filepath.Join(dir, "args.go")
		b, err := os.ReadFile(path)
		if err != nil {
			return "", 0, err
		}
		s := string(b)
		old := "\t\tcase a == \"--timeout\":"
		added := "\t\tcase strings.HasPrefix(a, \"--timeout=\"):\n" +
			"\t\t\to.Timeout = parseSeconds(strings.TrimPrefix(a, \"--timeout=\"))\n" + old
		s = strings.Replace(s, old, added, 1)
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			return "", 0, err
		}
		return "补上等号形式", 42, nil
	}
	rep := RunAll(context.Background(), []Task{task}, agent, 2*time.Minute)
	if rep.Pass != 1 || rep.Fail != 0 {
		t.Fatalf("argsfix 应通过：%+v", rep.Results)
	}
	if rep.Results[0].Tokens != 42 || rep.Results[0].Ms == 0 {
		t.Fatalf("用量/耗时未记录：%+v", rep.Results[0])
	}
}

func TestRunSlug(t *testing.T) {
	task := taskByID(t, "slug")
	agent := func(_ context.Context, dir, _ string) (string, int, error) {
		path := filepath.Join(dir, "slug.go")
		body := "package slug\n\nimport \"strings\"\n\nfunc Slug(s string) string {\n\treturn strings.Join(strings.Fields(strings.ToLower(s)), \"-\")\n}\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return "", 0, err
		}
		return "按空白切分", 7, nil
	}
	rep := RunAll(context.Background(), []Task{task}, agent, 2*time.Minute)
	if rep.Pass != 1 {
		t.Fatalf("slug 应通过：%+v", rep.Results)
	}
}

func TestRunDedupe(t *testing.T) {
	task := taskByID(t, "dedupe")
	agent := func(_ context.Context, dir, _ string) (string, int, error) {
		path := filepath.Join(dir, "dedupe.go")
		b, err := os.ReadFile(path)
		if err != nil {
			return "", 0, err
		}
		s := strings.Replace(string(b),
			"\tfor _, s := range in {\n\t\tout = append(out, s)",
			"\tfor _, s := range in {\n\t\tif seen[s] {\n\t\t\tcontinue\n\t\t}\n\t\tout = append(out, s)", 1)
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			return "", 0, err
		}
		return "补 seen 判断", 5, nil
	}
	rep := RunAll(context.Background(), []Task{task}, agent, 2*time.Minute)
	if rep.Pass != 1 {
		t.Fatalf("dedupe 应通过：%+v", rep.Results)
	}
}

func TestRunJSONField(t *testing.T) {
	task := taskByID(t, "jsonfield")
	agent := func(_ context.Context, dir, _ string) (string, int, error) {
		path := filepath.Join(dir, "user.go")
		b, err := os.ReadFile(path)
		if err != nil {
			return "", 0, err
		}
		s := strings.Replace(string(b), `json:"email_address,omitempty"`, `json:"email,omitempty"`, 1)
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			return "", 0, err
		}
		return "修正 JSON tag", 3, nil
	}
	rep := RunAll(context.Background(), []Task{task}, agent, 2*time.Minute)
	if rep.Pass != 1 {
		t.Fatalf("jsonfield 应通过：%+v", rep.Results)
	}
}

func TestRejectsTestModification(t *testing.T) {
	task := taskByID(t, "argsfix")
	agent := func(_ context.Context, dir, _ string) (string, int, error) {
		// 直接改测试来“通过”必须被哈希校验拦住。
		path := filepath.Join(dir, "args_test.go")
		return "", 0, os.WriteFile(path, []byte("package argsfix\n"), 0o644)
	}
	rep := RunAll(context.Background(), []Task{task}, agent, 2*time.Minute)
	if rep.Fail != 1 || !strings.Contains(rep.Results[0].Detail, "受保护") {
		t.Fatalf("改测试应被拒绝：%+v", rep.Results)
	}
}

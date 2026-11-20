package bench

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// 生成的 fixture 必须本身能编译、且初始测试确实是失败的；否则 bench 成绩无效。
func TestGeneratedTasksInitialFail(t *testing.T) {
	if _, err := exec.LookPath(goCommand()); err != nil {
		t.Skipf("go 不在 PATH：%v", err)
	}
	tasks := generatedTasks()
	if len(tasks) < 15 {
		t.Fatalf("生成任务过少: %d", len(tasks))
	}
	for _, task := range tasks {
		t.Run(task.ID, func(t *testing.T) {
			dir := t.TempDir()
			if err := writeTaskFiles(dir, task.Files); err != nil {
				t.Fatal(err)
			}
			res, err := runGoTest(context.Background(), dir, 2*time.Minute)
			if err != nil {
				t.Fatalf("初始测试无法运行: %v\n%s", err, res.Output)
			}
			if res.Code == 0 {
				t.Fatalf("fixture 初始测试不应通过:\n%s", res.Output)
			}
		})
	}
}

package core

import (
	"bytes"
	"os/exec"
	"strings"
)

// GitRun 在工作目录 dir 执行一条 git 命令，返回合并后的标准输出。
// 用于 /diff、/commit 等本地版本管理操作，不经过模型。
func GitRun(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	hideWindow(cmd)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	res := out.String()
	if errb.Len() > 0 {
		if res != "" && !strings.HasSuffix(res, "\n") {
			res += "\n"
		}
		res += errb.String()
	}
	return res, err
}

// GitAvailable 判断当前机器是否装了 git。
func GitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

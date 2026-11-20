package remote

import "win7harness/internal/core"

// 会话读取的薄包装。
//
// 手机端要看的会话列表与内容，其格式由 core 定义（磁盘契约）。
// 这里只做转发，好处是 remote 包对外仍只暴露自己的类型，
// 上层（app）与自检都能直接换掉 SessionDir 来测试。

func sessionIndex(dir string, limit int) ([]core.SessionBrief, error) {
	return core.SessionIndex(dir, limit)
}

func sessionLines(dir, id string, from, max int) ([]core.SessionLine, int, error) {
	return core.SessionLines(dir, id, from, max)
}

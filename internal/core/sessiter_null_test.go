package core

import (
	"strings"
	"testing"
)

// 旧版本空会话会写成 "msgs":null；必须当成 0 条消息，而不是坏文件。
func TestScanSessionLiteAcceptsNullMsgs(t *testing.T) {
	raw := `{"v":1,"conv":{"id":"abc","title":"新对话","at":"2026-11-19T12:00:00+08:00","msgs":null}}`
	res, err := scanSessionLite(strings.NewReader(raw), -1, 0)
	if err != nil {
		t.Fatalf("msgs:null 不应报错: %v", err)
	}
	if res.ID != "abc" || res.Msgs != 0 {
		t.Fatalf("解析结果异常: %+v", res)
	}
}

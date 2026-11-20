//go:build windows && (amd64 || 386)

package tools

import (
	"strings"
	"testing"
)

func TestMergeEnvWindowsLastWinsCaseInsensitive(t *testing.T) {
	got := mergeEnvWindows(
		[]string{"HTTP_PROXY=http://old", "PATH=C:\\Windows"},
		[]string{"http_proxy=http://bridge:1234", "ALL_PROXY=http://bridge:1234"},
		[]string{"HTTPS_PROXY=http://bridge:1234"},
	)
	want := map[string]string{
		"HTTP_PROXY":  "http://bridge:1234",
		"PATH":        "C:\\Windows",
		"ALL_PROXY":   "http://bridge:1234",
		"HTTPS_PROXY": "http://bridge:1234",
	}
	seen := map[string]string{}
	for _, kv := range got {
		if i := strings.IndexByte(kv, '='); i > 0 {
			seen[strings.ToUpper(kv[:i])] = kv[i+1:]
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("环境变量数量不对：%v", seen)
	}
	for k, v := range want {
		if seen[k] != v {
			t.Fatalf("%s = %q，期望 %q", k, seen[k], v)
		}
	}
}

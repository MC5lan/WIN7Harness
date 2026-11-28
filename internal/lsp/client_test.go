package lsp

import (
	"bufio"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathURIRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "中文 文件.go")
	uri := PathToURI(p)
	got := URIToPath(uri)
	want, _ := filepath.Abs(p)
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("round trip = %q, want %q (uri=%s)", got, want, uri)
	}
}

func TestDecodeLocationsObjectAndArray(t *testing.T) {
	one := `{"uri":"file:///C:/a.go","range":{"start":{"line":1,"character":2}}}`
	if locs := decodeLocations(json.RawMessage(one)); len(locs) != 1 || locs[0].Range.Start.Line != 1 {
		t.Fatalf("object 解析失败: %+v", locs)
	}
	many := `[{"uri":"file:///C:/a.go","range":{"start":{"line":1,"character":2}}},{"uri":"file:///C:/b.go","range":{"start":{"line":3,"character":4}}}]`
	if locs := decodeLocations(json.RawMessage(many)); len(locs) != 2 {
		t.Fatalf("array 解析失败: %+v", locs)
	}
}

func TestRenderHover(t *testing.T) {
	if got := renderHover(map[string]interface{}{"kind": "plaintext", "value": "func F()"}); got != "func F()" {
		t.Fatalf("hover = %q", got)
	}
}

func TestUnmarshalRPCResponse(t *testing.T) {
	var msg struct {
		ID *int
		rpcResponse
	}
	if err := json.Unmarshal([]byte(`{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.ID == nil || *msg.ID != 7 {
		t.Fatalf("id 未解析: %+v", msg.ID)
	}
	if len(msg.Result) == 0 || !strings.Contains(string(msg.Result), "ok") {
		t.Fatalf("result 未解析: %s", msg.Result)
	}
}

// 恶意/损坏的语言服务器用超大 Content-Length 可以让 32 位进程直接 OOM。
func TestReadFrameRejectsOversize(t *testing.T) {
	c := &Client{out: bufio.NewReader(strings.NewReader("Content-Length: 999999999\r\n\r\n"))}
	if _, err := c.readFrame(); err == nil {
		t.Fatal("超大帧应被拒绝")
	}
}

func TestReadFrameRejectsLongHeader(t *testing.T) {
	c := &Client{out: bufio.NewReader(strings.NewReader("X-Long: " + strings.Repeat("a", 9000) + "\r\n\r\n"))}
	if _, err := c.readFrame(); err == nil {
		t.Fatal("超长头部行应被拒绝")
	}
}

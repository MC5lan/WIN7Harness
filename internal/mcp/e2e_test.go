package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"win7harness/internal/core"
)

// fakeServer 是一个最小 MCP 服务，用于协议连通性测试。
const fakeServer = `import json,sys
def send(o):
    sys.stdout.write(json.dumps(o)+"\n"); sys.stdout.flush()
for line in sys.stdin:
    line=line.strip()
    if not line: continue
    m=json.loads(line)
    mid=m.get("id"); method=m.get("method","")
    if method=="initialize":
        send({"jsonrpc":"2.0","id":mid,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"fake","version":"1"}}})
    elif method=="notifications/initialized":
        pass
    elif method=="tools/list":
        send({"jsonrpc":"2.0","id":mid,"result":{"tools":[{"name":"echo","description":"回显","inputSchema":{"type":"object","properties":{"text":{"type":"string"}}}}]}})
    elif method=="tools/call":
        a=m.get("params",{}).get("arguments",{})
        send({"jsonrpc":"2.0","id":mid,"result":{"content":[{"type":"text","text":"echo:"+str(a.get("text",""))}]}})
    else:
        send({"jsonrpc":"2.0","id":mid,"error":{"code":-32601,"message":"unknown"}})
`

func TestClientEcho(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("端到端测试只在开发机（非 Windows）运行")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("未安装 python3，跳过")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake_mcp.py")
	if err := os.WriteFile(script, []byte(fakeServer), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Start(core.MCPServer{Name: "fake", Command: py, Args: []string{script}, Enabled: true})
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	defer c.Close()

	list, err := c.ListTools()
	if err != nil {
		t.Fatalf("tools/list 失败: %v", err)
	}
	if len(list) != 1 || list[0].Name != "echo" {
		t.Fatalf("工具清单异常: %+v", list)
	}
	out, err := c.CallTool("echo", map[string]interface{}{"text": "hi"})
	if err != nil {
		t.Fatalf("tools/call 失败: %v", err)
	}
	if out != "echo:hi" {
		t.Fatalf("返回内容异常: %q", out)
	}
}

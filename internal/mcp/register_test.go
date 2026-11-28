package mcp

import "testing"

func TestToolNameFor(t *testing.T) {
	cases := []struct{ server, tool, want string }{
		{"fs", "read_file", "mcp_fs_read_file"},
		{"my.server", "dir/list", "mcp_my_server_dir_list"},
		{"fs", "a b", "mcp_fs_a_b"},
	}
	for _, c := range cases {
		if got := toolNameFor(c.server, c.tool); got != c.want {
			t.Errorf("toolNameFor(%q,%q)=%q want %q", c.server, c.tool, got, c.want)
		}
	}
	if got := toolNameFor("srv", string(make([]byte, 100))); len(got) > 64 {
		t.Errorf("名称超长未截断: %d", len(got))
	}
}

func TestMCPBudget(t *testing.T) {
	b := newMCPBudget()
	if !b.take(1024, 100) {
		t.Fatal("首个小工具应被接受")
	}
	if b.tools != mcpMaxToolsTotal-1 || b.schema != mcpMaxSchemaTotal-1024 {
		t.Fatalf("预算扣减异常：%+v", b)
	}
	if b.take(mcpMaxSchemaTotal, 1) {
		t.Fatal("超过剩余 schema 预算应拒绝")
	}
	if b.skipped != 1 {
		t.Fatalf("skipped=%d want 1", b.skipped)
	}

	b2 := newMCPBudget()
	for i := 0; i < mcpMaxToolsTotal; i++ {
		if !b2.take(1, 1) {
			t.Fatalf("第 %d 个工具应被接受", i)
		}
	}
	if b2.take(1, 1) {
		t.Fatal("超过工具总数上限应拒绝")
	}
}

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

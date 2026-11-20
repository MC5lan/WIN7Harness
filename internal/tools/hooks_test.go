package tools

import "testing"

func TestHookMatch(t *testing.T) {
	cases := []struct {
		pattern, tool string
		want          bool
	}{
		{"*", "read_file", true},
		{"", "read_file", true},
		{"write_file", "write_file", true},
		{"write_file", "edit_file", false},
		{"write_*", "write_file", true},
		{"write_*,edit_file", "edit_file", true},
		{" write_file , edit_file ", "edit_file", true},
		{"run_*", "read_file", false},
	}
	for _, c := range cases {
		if got := hookMatch(c.pattern, c.tool); got != c.want {
			t.Errorf("hookMatch(%q,%q)=%v want %v", c.pattern, c.tool, got, c.want)
		}
	}
}

func TestHooksSummaryEmpty(t *testing.T) {
	if got := HooksSummary(); len(got) != 0 {
		t.Errorf("默认应无钩子，实际 %d 个", len(got))
	}
}

package tools

import "testing"

func TestIsReadOnly(t *testing.T) {
	for _, n := range []string{"read_file", "list_dir", "glob", "grep", "todo_write", "task"} {
		if !IsReadOnly(n) {
			t.Fatalf("%s 应为只读", n)
		}
	}
	for _, n := range []string{"write_file", "edit_file", "run_command", "delete_path"} {
		if IsReadOnly(n) {
			t.Fatalf("%s 不应为只读", n)
		}
	}
}

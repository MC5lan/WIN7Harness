package core

import (
	"strings"
	"testing"
)

func TestDiffHunksStructure(t *testing.T) {
	old := "package main\n\nfunc main() {\n\tprintln(\"old\")\n}\n"
	new := "package main\n\nfunc main() {\n\tprintln(\"new\")\n}\n"
	hunks := DiffHunks(old, new, "main.go", 1)
	if len(hunks) != 1 {
		t.Fatalf("want 1 hunk, got %d: %+v", len(hunks), hunks)
	}
	h := hunks[0]
	if !strings.Contains(h.Title, "@@ -") || !strings.Contains(h.Title, "main.go") {
		t.Errorf("hunk title should carry line numbers and path: %q", h.Title)
	}
	var plus, minus int
	for _, l := range h.Lines {
		switch l.Prefix {
		case '+':
			plus++
			if l.NewNo <= 0 || l.OldNo != 0 {
				t.Errorf("added line must have NewNo and no OldNo: %+v", l)
			}
		case '-':
			minus++
			if l.OldNo <= 0 || l.NewNo != 0 {
				t.Errorf("removed line must have OldNo and no NewNo: %+v", l)
			}
		}
	}
	if plus != 1 || minus != 1 {
		t.Errorf("want 1 added + 1 removed, got +%d/-%d: %+v", plus, minus, h.Lines)
	}
}

func TestDiffHunksNoChangeAndMultiHunk(t *testing.T) {
	if got := DiffHunks("same\n", "same\n", "a.go", 3); got != nil {
		t.Errorf("identical text should yield no hunks, got %+v", got)
	}
	old := strings.Join([]string{
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j",
		"k", "l", "m", "n", "o", "p", "q", "r", "s", "t",
	}, "\n")
	newText := strings.Replace(old, "b", "B", 1)
	newText = strings.Replace(newText, "s", "S", 1)
	hunks := DiffHunks(old, newText, "x.go", 0)
	if len(hunks) != 2 {
		t.Fatalf("want 2 hunks for 2 distant changes, got %d: %+v", len(hunks), hunks)
	}
	for _, h := range hunks {
		if len(h.Lines) == 0 {
			t.Error("hunk has no lines")
		}
	}
}

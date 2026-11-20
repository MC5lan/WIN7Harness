package argsfix

import "testing"

func TestParseSeparatedForm(t *testing.T) {
	o := Parse([]string{"-run", "x", "--timeout", "90"})
	if o.Prompt != "x" || o.Timeout != 90 {
		t.Fatalf("分离形式解析错误: %+v", o)
	}
}

func TestParseTimeoutEqualsForm(t *testing.T) {
	o := Parse([]string{"-run", "x", "--timeout=90"})
	if o.Timeout != 90 {
		t.Fatalf("Timeout = %d, want 90", o.Timeout)
	}
}

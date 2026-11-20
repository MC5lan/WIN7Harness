package dedupe

import (
	"reflect"
	"testing"
)

func TestUniqueKeepsFirstOrder(t *testing.T) {
	got := Unique([]string{"a", "b", "a", "c", "b"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Unique = %v, want %v", got, want)
	}
}

func TestUniqueEmpty(t *testing.T) {
	if got := Unique(nil); len(got) != 0 {
		t.Fatalf("Unique(nil) = %v, want empty", got)
	}
}

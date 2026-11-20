package slug

import "testing"

func TestSlugNormalizesWhitespace(t *testing.T) {
	cases := map[string]string{
		"  Hello  World  ": "hello-world",
		"Go 1.20":           "go-1.20",
		"A--B":              "a--b",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Fatalf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

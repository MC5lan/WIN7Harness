package jsonfield

import "testing"

func TestEncodeUsesEmailField(t *testing.T) {
	got, err := Encode(User{Name: "Ada", Email: "ada@example.com", Age: 36})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"Ada","email":"ada@example.com","age":36}`
	if got != want {
		t.Fatalf("Encode = %s, want %s", got, want)
	}
}

func TestEncodeOmitsEmptyEmail(t *testing.T) {
	got, err := Encode(User{Name: "Ada", Age: 36})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"Ada","age":36}`
	if got != want {
		t.Fatalf("Encode = %s, want %s", got, want)
	}
}

package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListModelsParsesOpenAIShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-a"},{"id":"model-b"},{"id":"model-a"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL+"/v1", "sk-test", "", 10)
	got, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "model-a" || got[1] != "model-b" {
		t.Fatalf("models = %v", got)
	}
}

func TestListModelsReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"no models endpoint"}}`, http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL+"/v1", "", "", 10)
	if _, err := c.ListModels(context.Background()); err == nil {
		t.Fatal("404 应返回错误")
	}
}

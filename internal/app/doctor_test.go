//go:build windows

package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"win7harness/internal/core"
)

func TestCmdDoctorProducesReport(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["tools"]; ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"tools not supported (test)"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("WIN7HARNESS_HOME", t.TempDir())
	cfg := core.DefaultConfig()
	cfg.Onboarded = true
	cfg.Providers = []core.Provider{{
		ID:      "custom",
		Name:    "test",
		BaseURL: srv.URL + "/v1",
		APIKey:  "sk-test-123456",
		Model:   "m",
	}}
	cfg.ActiveProvider = 0
	cfg.Model = "m"

	a := &App{
		cfg:      cfg,
		convs:    []*Conv{{ID: "c1", Title: "test", Loaded: true}},
		active:   0,
		doctorCh: make(chan testResult, 1),
	}
	a.cmdDoctor()
	select {
	case res := <-a.doctorCh:
		if !res.ok {
			t.Fatalf("doctor 应通过基础对话：%s", res.msg)
		}
		for _, want := range []string{"端点诊断", "HTTP 200", "tools not supported"} {
			if !strings.Contains(res.msg, want) {
				t.Fatalf("报告缺少 %q：\n%s", want, res.msg)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("doctor 超时，没有投递报告")
	}
}

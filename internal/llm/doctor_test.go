package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDoctorReportsChatAndTools(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"},{"id":"m2"}]}`))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["tools"]; ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"tools not supported"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL+"/v1", "sk-test", "m1", 5*time.Second)
	rep := c.Doctor(context.Background())
	if rep.Models.Status != 200 {
		t.Fatalf("/models status = %d", rep.Models.Status)
	}
	if rep.Chat.Status != 200 {
		t.Fatalf("chat status = %d, body=%s", rep.Chat.Status, rep.Chat.Body)
	}
	if rep.Tools.Status != 400 {
		t.Fatalf("tools status = %d", rep.Tools.Status)
	}
	if !rep.OK() {
		t.Fatal("基础对话通过时 OK 应为 true")
	}
	if !strings.Contains(rep.Text(), "tools not supported") {
		t.Fatalf("报告里没有上游原文：%s", rep.Text())
	}
}

func TestCheckChatExposesUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"MissingSessionID"}}`))
	}))
	defer srv.Close()
	c := New(srv.URL+"/v1", "sk-test", "m1", 5*time.Second)
	err := c.CheckChat(context.Background())
	if err == nil || !strings.Contains(err.Error(), "MissingSessionID") {
		t.Fatalf("CheckChat 应带上游原文，实际 %v", err)
	}
}

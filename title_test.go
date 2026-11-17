package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTitleURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://service.example", "https://service.example/v1/chat/completions"},
		{"https://service.example/v1/", "https://service.example/v1/chat/completions"},
		{"https://service.example/api/v2", "https://service.example/api/v2/chat/completions"},
		{"https://service.example/v1/chat/completions/", "https://service.example/v1/chat/completions"},
	} {
		got, err := titleURL(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("%s: got %s %v", tc.in, got, err)
		}
	}
	for _, endpoint := range []string{"", "https://key@service.example", "https://service.example?token=secret"} {
		if _, err := titleURL(endpoint); err == nil {
			t.Fatalf("accepted invalid endpoint %q", endpoint)
		}
	}
}

func TestTitleServiceOriginAndFullEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "website", 404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing credential")
		}
		var body struct {
			Model    string              `json:"model"`
			Messages []map[string]string `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "test-model" || len(body.Messages) != 2 {
			t.Error("invalid request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"连接测试"}}]}`))
	}))
	defer server.Close()
	d := &Desktop{}
	for _, suffix := range []string{"", "/v1", "/v1/chat/completions"} {
		if err := d.TestTitleService(server.URL+suffix, "test-model", "test-secret"); err != nil {
			t.Fatalf("%q: %v", suffix, err)
		}
	}
}

func TestTitleServiceFailures(t *testing.T) {
	for _, tc := range []struct {
		name           string
		status         int
		body, contains string
	}{
		{"website", 200, "<html>private provider content</html>", "Chat Completions"},
		{"empty", 200, `{"choices":[{"message":{"content":""}}]}`, "未返回标题"},
		{"auth", 401, `{"error":{"message":"private provider content"}}`, "密钥"},
		{"model", 404, `{}`, "模型"},
		{"quota", 429, `{}`, "配额"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			_, err := generateTitle(context.Background(), Settings{TitleEndpoint: server.URL, TitleModel: "test-model"}, "synthetic test", "test-secret")
			if err == nil || !strings.Contains(err.Error(), tc.contains) || strings.Contains(err.Error(), "private provider content") || strings.Contains(err.Error(), "test-secret") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

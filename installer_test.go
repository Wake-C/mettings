package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallerVerifiesBeforeActivating(t *testing.T) {
	content := []byte("native model fixture")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(content) }))
	defer server.Close()
	c := component{ID: "fixture", URL: server.URL, SHA: "wrong", Format: "file", Files: map[string]string{"model": "model.onnx"}}
	root := t.TempDir()
	if installComponent(context.Background(), root, c, func(int64, int64) {}) == nil {
		t.Fatal("accepted wrong checksum")
	}
	if installed(root, c) {
		t.Fatal("marked failed install ready")
	}
	if _, err := os.Stat(filepath.Join(root, "model.onnx")); !os.IsNotExist(err) {
		t.Fatal("activated unverified model")
	}
	c.SHA = fmt.Sprintf("%x", sha256.Sum256(content))
	if err := installComponent(context.Background(), root, c, func(int64, int64) {}); err != nil {
		t.Fatal(err)
	}
	if !installed(root, c) {
		t.Fatal("not ready after successful install")
	}
}

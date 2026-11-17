package meeting

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadSignedAudioURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "fixture" {
			t.Error("signed URL query lost")
		}
		w.Write([]byte("sample audio"))
	}))
	defer server.Close()
	root := t.TempDir()
	name, err := DownloadAudio(context.Background(), server.URL+"/meeting.m4a?token=fixture", root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, name))
	if err != nil || string(b) != "sample audio" {
		t.Fatal("download not preserved")
	}
}
func TestDownloadFailureLeavesNoRecording(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	root := t.TempDir()
	if _, err := DownloadAudio(context.Background(), server.URL+"/audio.mp3", root); err == nil {
		t.Fatal("404 accepted")
	}
	files, _ := os.ReadDir(root)
	if len(files) != 0 {
		t.Fatal("failed download left an audio file")
	}
}

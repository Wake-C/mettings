package main

import (
	"context"
	"mettings.local/desktop/internal/meeting"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativePipelineKeepsAudioAndResumes(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":"麦狗讨论记录"}`))
	}))
	defer server.Close()
	s := defaultSettings()
	s.Root = t.TempDir()
	s.ASR = meeting.Provider{Kind: "compatible", Endpoint: server.URL, Model: "mock"}
	s.Corrections = "麦狗=Mygo"
	store := meeting.Store{Root: s.Root}
	item, err := store.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	audio := filepath.Join(s.Root, item.Directory, item.Audio)
	os.WriteFile(audio, []byte("synthetic audio"), 0600)
	d := &Desktop{settings: s}
	if err = d.pipeline(context.Background(), item, s, meeting.Credentials{}, ""); err == nil {
		t.Fatal("503 should fail")
	}
	item, err = store.Find(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Transcribed || item.Error == "" {
		t.Fatalf("wrong checkpoint %+v", item)
	}
	if err = d.pipeline(context.Background(), item, s, meeting.Credentials{}, ""); err != nil {
		t.Fatal(err)
	}
	item, err = store.Find(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !item.Summarized || item.Error != "" {
		t.Fatalf("not archived %+v", item)
	}
	if err = d.pipeline(context.Background(), item, s, meeting.Credentials{}, ""); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("completed stage reran")
	}
	dir := filepath.Join(s.Root, item.Directory)
	b, err := os.ReadFile(filepath.Join(dir, item.Audio))
	if err != nil || string(b) != "synthetic audio" {
		t.Fatal("original audio lost")
	}
	b, err = os.ReadFile(filepath.Join(dir, "transcript.md"))
	if err != nil || !strings.Contains(string(b), "Mygo") {
		t.Fatal("correction not applied")
	}
	if item.Title != "Mygo讨论记录" {
		t.Fatal(item.Title)
	}
}
func TestLabelExistingVoiceAppliesAliasAndLearnsSample(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	dir := t.TempDir()
	os.MkdirAll(filepath.Dir(voiceLibrary()), 0700)
	meeting.WriteJSON(voiceLibrary(), VoiceLibrary{Model: voiceModelID, Voices: map[string][]float64{"许宁": {1, 0}}})
	meeting.WriteJSON(filepath.Join(dir, "unidentified.json"), []UnknownSpeaker{{Speaker: "1", Segment: "clip.wav"}})
	os.WriteFile(filepath.Join(dir, "transcript.md"), []byte("**[00:00:01] 1**：1 个方案\n"), 0600)
	s := defaultSettings()
	s.Aliases = `{"小许":"许宁"}`
	if err := labelVoiceWithEmbed(context.Background(), s, dir, "1", "小许", func(context.Context, string) ([]float64, error) { return []float64{0, 1}, nil }); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "transcript.md"))
	if string(b) != "**[00:00:01] 许宁**：1 个方案\n" {
		t.Fatal(string(b))
	}
	mapping, err := os.ReadFile(filepath.Join(dir, "speaker_map.json"))
	if err != nil || !strings.Contains(string(mapping), "许宁") {
		t.Fatal("manual speaker label was not saved to the mapping")
	}
	library, err := loadVoiceLibrary()
	if err != nil || matchVoice([]float64{0, 1}, library, .6) != "许宁" {
		t.Fatal("alias enrollment did not add new sample to canonical name")
	}
}

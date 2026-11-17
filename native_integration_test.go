package main

import (
	"context"
	"encoding/json"
	"mettings.local/desktop/internal/meeting"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in: runs the shipped native helper and real models against public sample audio.
// Fixtures are pinned downloads, not the user's recordings or voiceprint library.
func TestNativeOfflineWorkflow(t *testing.T) {
	fixtures := os.Getenv("METTINGS_NATIVE_FIXTURES")
	if fixtures == "" {
		t.Skip("set METTINGS_NATIVE_FIXTURES to the downloaded fixture directory")
	}
	model := os.Getenv("METTINGS_NATIVE_MODEL")
	if model == "" {
		model = "sensevoice"
	}
	data := t.TempDir()
	t.Setenv("METTINGS_DATA_DIR", data)
	server := httptest.NewServer(http.FileServer(http.Dir(fixtures)))
	defer server.Close()
	archives := map[string]string{"firered2": "firered2.tar.bz2", "qwen3": "qwen3-asr.tar.bz2", "ffmpeg": "ffmpeg.zip", "sensevoice": "sensevoice.tar.bz2", "campplus": "campplus.onnx", "segmentation": "segmentation.tar.bz2"}
	for _, c := range componentsForModel(model) {
		if strings.HasPrefix(c.ID, "qwen17-") {
			c.URL = server.URL + "/models/" + c.Files["model"]
		} else {
			c.URL = server.URL + "/" + archives[c.ID]
		}
		if err := installComponent(context.Background(), runtimeDir(), c, func(int64, int64) {}); err != nil {
			t.Fatalf("install %s: %v", c.ID, err)
		}
	}
	if !runtimeReady(model) {
		t.Fatal("wizard runtime not ready")
	}
	sample := filepath.Join(fixtures, "models", "sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2025-09-09", "test_wavs", "zh.wav")
	audio, err := os.ReadFile(sample)
	if err != nil {
		t.Fatal(err)
	}
	s := defaultSettings()
	s.LocalModel = model
	s.Root = filepath.Join(data, "sessions")
	store := meeting.Store{Root: s.Root}
	d := &Desktop{settings: s}
	create := func() meeting.Session {
		item, e := store.Create(time.Now())
		if e != nil {
			t.Fatal(e)
		}
		item.Audio = "recording.wav"
		if e = store.Save(item); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(s.Root, item.Directory, item.Audio), audio, 0600); e != nil {
			t.Fatal(e)
		}
		return item
	}
	item := create()
	if err = d.pipeline(context.Background(), item, s, meeting.Credentials{}, ""); err != nil {
		t.Fatal(err)
	}
	item, err = store.Find(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Root, item.Directory)
	b, _ := os.ReadFile(filepath.Join(dir, "transcript.md"))
	if !strings.Contains(string(b), "时间") && !strings.Contains(string(b), "時間") {
		t.Fatalf("unexpected public sample transcript: %s", b)
	}
	var unknown []UnknownSpeaker
	b, _ = os.ReadFile(filepath.Join(dir, "unidentified.json"))
	if json.Unmarshal(b, &unknown) != nil || len(unknown) == 0 {
		t.Fatal("no speaker clip to label")
	}
	// A transcript created before enrollment must also be able to use the
	// shared library later, without running ASR again.
	historical := create()
	if err = d.pipeline(context.Background(), historical, s, meeting.Credentials{}, ""); err != nil {
		t.Fatal(err)
	}
	if err = d.LabelVersion(item.ID, "", unknown[0].Speaker, "测试声音"); err != nil {
		t.Fatal(err)
	}
	library, err := loadVoiceLibrary()
	if err != nil || len(library.Voices["测试声音"]) != 192 {
		t.Fatalf("wrong native voice library: %v", err)
	}
	historical, err = store.Find(historical.ID)
	if err != nil {
		t.Fatal(err)
	}
	historicalDir, err := meeting.RunDir(filepath.Join(s.Root, historical.Directory), historical.ActiveRun)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(historicalDir, "transcript.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = d.ReidentifySpeakers(historical.ID, ""); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(historicalDir, "transcript.json"))
	b, _ = os.ReadFile(filepath.Join(historicalDir, "transcript.md"))
	if string(before) != string(after) || !strings.Contains(string(b), "测试声音") {
		t.Fatal("historical rematch changed ASR output or missed the shared voice")
	}
	again := create()
	if err = d.pipeline(context.Background(), again, s, meeting.Credentials{}, ""); err != nil {
		t.Fatal(err)
	}
	again, err = store.Find(again.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(s.Root, again.Directory, "transcript.md"))
	if !strings.Contains(string(b), "测试声音") {
		t.Fatal("enrolled voice not recognized in the next session")
	}
	if !again.Summarized {
		t.Fatal("session not archived")
	}
	againDir, _ := meeting.RunDir(filepath.Join(s.Root, again.Directory), again.ActiveRun)
	// Simulate the older index which discarded automatically matched voices.
	if err = meeting.WriteJSON(filepath.Join(againDir, "unidentified.json"), []UnknownSpeaker{}); err != nil {
		t.Fatal(err)
	}
	if err = d.ReidentifySpeakers(again.ID, ""); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(againDir, "unidentified.json"))
	if json.Unmarshal(b, &unknown) != nil || len(unknown) == 0 || unknown[0].Name != "测试声音" || unknown[0].Labeled {
		t.Fatal("historical automatic speaker not restored for review")
	}
	if err = d.LabelVersion(again.ID, "", unknown[0].Speaker, "测试声音"); err != nil {
		t.Fatal(err)
	}
}

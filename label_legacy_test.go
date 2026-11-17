package main

import (
	"context"
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLabelVirtualLegacyVersionUpdatesRootWithoutError(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	s := defaultSettings()
	s.Root = t.TempDir()
	store := meeting.Store{Root: s.Root}
	item, err := store.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	item.Transcribed = true
	item.Warning = "部分说话人尚未识别。可播放片段并标注姓名。"
	store.Save(item)
	dir := filepath.Join(s.Root, item.Directory)
	os.WriteFile(filepath.Join(dir, "transcript.md"), []byte("**[00:00:01] 1**：你好"), 0600)
	meeting.WriteJSON(filepath.Join(dir, "unidentified.json"), []UnknownSpeaker{{Speaker: "1", Segment: "clip.wav"}})
	os.MkdirAll(filepath.Dir(voiceLibrary()), 0700)
	meeting.WriteJSON(voiceLibrary(), VoiceLibrary{Model: voiceModelID, Voices: map[string][]float64{"示例声音": {1, 0}}})
	d := &Desktop{settings: s}
	if err = d.labelVersion(item.ID, "legacy", "1", "示例声音", func(context.Context, string) ([]float64, error) { return []float64{1, 0}, nil }); err != nil {
		t.Fatal(err)
	}
	got, err := store.Find(item.ID)
	if err != nil || got.PendingSpeakers != 0 || got.Warning != "" {
		t.Fatalf("label not reflected %+v %v", got, err)
	}
}

package main

import (
	"encoding/json"
	"mettings.local/desktop/internal/desk"
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoricalDeskTimesAgreeAcrossDetailPreviewAndSpeakerRewrite(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	s := defaultSettings()
	s.Root = t.TempDir()
	store := meeting.Store{Root: s.Root}
	item, err := store.Create(time.Now().Add(-4 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	item.Mode = "desk"
	if err = store.Save(item); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Root, item.Directory)
	if err = os.MkdirAll(filepath.Join(dir, "clips"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err = desk.Append(filepath.Join(dir, "clips", "000001.wav"), make([]byte, 96000)); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-4 * time.Hour).Truncate(time.Second)
	clips := []DeskClip{{Index: 1, Path: "clips/000001.wav", StartMS: 0, EndMS: 3000, DurationMS: 3000, StartedAt: old.Format(time.RFC3339Nano)}}
	if err = meeting.WriteJSON(filepath.Join(dir, "clips.json"), clips); err != nil {
		t.Fatal(err)
	}
	var result meeting.Result
	result.Result.Utterances = []meeting.Utterance{{Start: 1000, End: 2000, Text: "test", RecordedAt: old.Add(time.Second).Format(time.RFC3339Nano), Additions: meeting.Speaker{Speaker: "1"}}}
	if err = meeting.WriteJSON(filepath.Join(dir, "transcript.json"), result); err != nil {
		t.Fatal(err)
	}
	original := meeting.Markdown(result)
	if err = os.WriteFile(filepath.Join(dir, "transcript.md"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	d := Desktop{settings: s}
	detail, err := d.TranscriptDetail(item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !detail.TimestampEstimated || detail.Transcript == original {
		t.Fatal("historical detail retained wrong calendar timestamp")
	}
	preview, err := d.ReadArchiveFile(filepath.Join(item.Directory, "transcript.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview, detail.Transcript) || !strings.Contains(preview, "估算") {
		t.Fatalf("archive preview disagrees: detail=%q preview=%q", detail.Transcript, preview)
	}
	unchanged, _ := os.ReadFile(filepath.Join(dir, "transcript.md"))
	if string(unchanged) != original {
		t.Fatal("view mutated original transcript")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "clips.json"))
	var unchangedClips []DeskClip
	json.Unmarshal(before, &unchangedClips)
	if unchangedClips[0].StartedAt != clips[0].StartedAt {
		t.Fatal("view mutated capture metadata")
	}
	if err = writeSpeakerTranscript(dir, s, map[string]string{"1": "Alice"}); err != nil {
		t.Fatal(err)
	}
	updated, _ := os.ReadFile(filepath.Join(dir, "transcript.md"))
	if !strings.Contains(string(updated), "估算") || !strings.Contains(string(updated), "] Alice**") || strings.Contains(string(updated), old.Format("2006-01-02 15:04:05")) {
		t.Fatalf("speaker rewrite did not retain estimated timestamp: %q", updated)
	}
}

package main

import (
	"context"
	"encoding/json"
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavedSpeakersUseSharedLibraryWithoutASR(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(voiceLibrary()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := meeting.WriteJSON(voiceLibrary(), VoiceLibrary{Model: voiceModelID, Voices: map[string][]float64{"Alice": {1, 0}}}); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{t.TempDir(), t.TempDir()} {
		entries := []UnknownSpeaker{{Speaker: "1", Segment: "known.wav"}, {Speaker: "2", Segment: "unknown.wav"}, {Speaker: "3", Segment: "manual.wav", Labeled: true}}
		if err := meeting.WriteJSON(filepath.Join(root, "unidentified.json"), entries); err != nil {
			t.Fatal(err)
		}
		if err := meeting.WriteJSON(filepath.Join(root, "speaker_map.json"), map[string]string{"3": "Bob"}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "transcript.md"), []byte("**[00:00:01] 1**：hello\n**[00:00:02] 2**：unknown\n**[00:00:03] Bob**：manual"), 0600); err != nil {
			t.Fatal(err)
		}
		library, err := loadVoiceLibrary()
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		embed := func(_ context.Context, path string) ([]float64, error) {
			calls++
			switch filepath.Base(path) {
			case "known.wav":
				return []float64{1, 0}, nil
			case "unknown.wav":
				return []float64{0, 1}, nil
			default:
				t.Fatal("manual identity was re-matched")
				return nil, nil
			}
		}
		if err = rematchSpeakers(context.Background(), defaultSettings(), root, library, embed); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(root, "transcript.md"))
		if calls != 2 || !strings.Contains(string(b), "] Alice**") || !strings.Contains(string(b), "] 2**") || !strings.Contains(string(b), "] Bob**") {
			t.Fatalf("incorrect matching: calls=%d text=%s", calls, b)
		}
		b, _ = os.ReadFile(filepath.Join(root, "unidentified.json"))
		if err = json.Unmarshal(b, &entries); err != nil {
			t.Fatal(err)
		}
		if entries[0].Labeled || entries[0].Name != "Alice" || entries[1].Labeled || entries[1].Name != "" || !entries[2].Labeled {
			t.Fatalf("incorrect labels: %+v", entries)
		}
	}
}

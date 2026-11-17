package main

import (
	"encoding/json"
	"fmt"
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeskAppendKeepsCorrectedAutomaticSpeaker(t *testing.T) {
	store := meeting.Store{Root: t.TempDir()}
	item, err := store.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(store.Root, item.Directory)
	settings, _ := json.Marshal(Settings{Corrections: "speach=speech"})
	run, err := meeting.CreateRun(session, "test", "configured", settings, true)
	if err != nil {
		t.Fatal(err)
	}
	run.Warning = "部分说话人尚未识别。可播放片段并标注姓名。"
	run.Clips = []meeting.TranscriptClip{{Index: 1, State: "complete"}, {Index: 2, State: "complete", RetainedOffsetMS: 3000}}
	if err = meeting.SaveRun(session, run); err != nil {
		t.Fatal(err)
	}
	dir, _ := meeting.RunDir(session, run.ID)
	for _, index := range []int{1, 2} {
		clip := filepath.Join(dir, "clips", fmt.Sprintf("%06d", index))
		if err = os.MkdirAll(clip, 0700); err != nil {
			t.Fatal(err)
		}
		spk := fmt.Sprintf("片段%d-1", index)
		var result meeting.Result
		result.Result.Utterances = []meeting.Utterance{{Start: 0, End: 1000, Text: "speach", Additions: meeting.Speaker{Speaker: spk}}}
		for name, value := range map[string]any{"transcript.json": result, "speaker_map.json": map[string]string{spk: "Alice"}, "unidentified.json": []UnknownSpeaker{{Speaker: spk, Name: "Alice", Segment: "segments/voice.wav", Start: 0, End: 1000}}} {
			if err = meeting.WriteJSON(filepath.Join(clip, name), value); err != nil {
				t.Fatal(err)
			}
		}
		if err = os.WriteFile(filepath.Join(clip, "transcript.md"), []byte("# 转写稿\n\n**[00:00:00] Alice**：speech"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// The user corrected the first clip after its automatic match. Its source
	// markdown remains unchanged when the next clip reaches the run.
	if err = meeting.WriteJSON(filepath.Join(dir, "speaker_map.json"), map[string]string{"片段1-1": "Bob"}); err != nil {
		t.Fatal(err)
	}
	if err = meeting.WriteJSON(filepath.Join(dir, "unidentified.json"), []UnknownSpeaker{{Speaker: "片段1-1", Name: "Bob", Labeled: true}}); err != nil {
		t.Fatal(err)
	}
	d := &Desktop{settings: Settings{Root: store.Root}}
	if err = d.aggregateAutomatic(transcriptionJob{Root: store.Root, SessionID: item.ID, RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "transcript.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "**[00:00:00] Bob**：speech") || !strings.Contains(string(b), "**[00:00:03] Alice**：speech") || strings.Contains(string(b), "speach") {
		t.Fatalf("names, corrected text, or offsets lost: %s", b)
	}
	var entries []UnknownSpeaker
	b, err = os.ReadFile(filepath.Join(dir, "unidentified.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "Bob" || !entries[0].Labeled || entries[1].Name != "Alice" || entries[1].Labeled || entries[1].Start != 3000 {
		t.Fatalf("speaker metadata lost: %+v", entries)
	}
	if err = d.PublishTranscriptVersion(item.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	published, err := meeting.LoadRun(session, run.ID)
	if err != nil || published.Warning != "" {
		t.Fatalf("recognized speakers kept unknown warning: %q %v", published.Warning, err)
	}

}

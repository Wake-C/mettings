package main

import (
	"encoding/json"
	"fmt"
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeskAggregateUsesCachedSpeakerEvidenceWithoutModels(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(voiceLibrary()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := meeting.WriteJSON(voiceLibrary(), VoiceLibrary{Voices: map[string][]float64{"known": {1, 0}}, Model: voiceModelID}); err != nil {
		t.Fatal(err)
	}
	s := defaultSettings()
	s.Root = t.TempDir()
	store := meeting.Store{Root: s.Root}
	item, err := store.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(s.Root, item.Directory)
	raw, _ := json.Marshal(s)
	run, err := meeting.CreateRun(sessionDir, "test", "configured", raw, true)
	if err != nil {
		t.Fatal(err)
	}
	run.Clips = []meeting.TranscriptClip{{Index: 1, State: "complete"}, {Index: 2, State: "complete", RetainedOffsetMS: 2000}}
	if err = meeting.SaveRun(sessionDir, run); err != nil {
		t.Fatal(err)
	}
	dir, _ := meeting.RunDir(sessionDir, run.ID)
	for i, vector := range [][]float64{{.8, .6}, {.5, .8660254}} {
		index := i + 1
		spk := fmt.Sprintf("clip%d", index)
		work := filepath.Join(dir, "clips", fmt.Sprintf("%06d", index))
		os.MkdirAll(work, 0700)
		var result meeting.Result
		result.Result.Utterances = []meeting.Utterance{{Start: 0, End: 1000, Text: "synthetic", Additions: meeting.Speaker{Speaker: spk}}}
		for name, value := range map[string]any{"transcript.json": result, "unidentified.json": []UnknownSpeaker{{Speaker: spk, Segment: "missing.wav"}}, "speaker_map.json": map[string]string{}, "speaker_embeddings.json": map[string][]float64{spk: vector}} {
			if err = meeting.WriteJSON(filepath.Join(work, name), value); err != nil {
				t.Fatal(err)
			}
		}
	}
	d := Desktop{settings: s}
	if err = d.aggregateAutomatic(transcriptionJob{Root: s.Root, SessionID: item.ID, RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "speaker_map.json"))
	if err != nil {
		t.Fatal(err)
	}
	var names map[string]string
	json.Unmarshal(b, &names)
	if names["clip1"] != "known" || names["clip2"] != "known" {
		t.Fatal("cross-clip cached evidence not applied")
	}
	library, err := loadVoiceLibrary()
	if err != nil || len(library.Samples) != 0 {
		t.Fatal("aggregation enrolled inferred samples")
	}
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mettings.local/desktop/internal/meeting"
)

func TestDeskDatesPreserveGapsAndMidnightWithoutChangingAudioOffsets(t *testing.T) {
	clips := []DeskClip{
		{Index: 1, RetainedOffsetMS: 0, DurationMS: 1500, StartedAt: "2026-11-02T23:59:59+08:00"},
		{Index: 2, RetainedOffsetMS: 1500, DurationMS: 3000, StartedAt: "2026-11-03T00:15:00+08:00"},
	}
	var result meeting.Result
	result.Result.Utterances = []meeting.Utterance{{Start: 1200, End: 1400, Text: "first", Additions: meeting.Speaker{Speaker: "1"}}, {Start: 1500, End: 4000, Text: "second", Additions: meeting.Speaker{Speaker: "2"}}}
	stampDeskResult(&result, clips, -1)
	if result.Result.Utterances[0].RecordedAt != "2026-11-03T00:00:00.2+08:00" || result.Result.Utterances[1].RecordedAt != "2026-11-03T00:15:00+08:00" {
		t.Fatal("wall clock lost gap or date boundary")
	}
	if result.Result.Utterances[0].Start != 1200 || result.Result.Utterances[1].Start != 1500 || result.Result.Utterances[1].End != 4000 {
		t.Fatal("playback offsets changed")
	}
	md := meeting.MapSpeakers(meeting.Markdown(result), map[string]string{"1": "Alice", "2": "Bob"})
	if !strings.Contains(md, "**[2026-11-03 00:00:00] Alice**") || !strings.Contains(md, "**[2026-11-03 00:15:00] Bob**") {
		t.Fatal("date rendering or speaker mapping lost")
	}
	local := meeting.Result{}
	local.Result.Utterances = []meeting.Utterance{{Start: 500, End: 1000, Text: "clip"}}
	stampDeskResult(&local, clips, 2)
	if local.Result.Utterances[0].RecordedAt != "2026-11-03T00:15:00.5+08:00" || local.Result.Utterances[0].Start != 500 {
		t.Fatal("clip offset changed")
	}
}

func TestHistoricalDeskDatesUsePreciseOffsetsNotRoundedMarkdown(t *testing.T) {
	clips := []DeskClip{{Index: 1, RetainedOffsetMS: 0, DurationMS: 1500, StartedAt: "2026-11-02T11:00:00+08:00"}, {Index: 2, RetainedOffsetMS: 1500, DurationMS: 1500, StartedAt: "2026-11-02T12:00:00+08:00"}}
	md := "**[00:00:01] Alice**：first\n**[00:00:01] Bob**：second"
	got := datedDeskMarkdown(md, clips, []meeting.Utterance{{Start: 1000}, {Start: 1500}})
	if !strings.Contains(got, "[2026-11-02 11:00:01] Alice") || !strings.Contains(got, "[2026-11-02 12:00:00] Bob") {
		t.Fatal("rounded offsets mapped to wrong side of gap")
	}
	if datedDeskMarkdown(md, clips, nil) != md {
		t.Fatal("rewrote unknown transcript structure")
	}
}

func TestDeskAggregatePersistsDatesForLaterSpeakerCorrection(t *testing.T) {
	store := meeting.Store{Root: t.TempDir()}
	item, err := store.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(store.Root, item.Directory)
	run, err := meeting.CreateRun(session, "model", "qwen3", json.RawMessage(`{}`), true)
	if err != nil {
		t.Fatal(err)
	}
	run.Clips = []meeting.TranscriptClip{{Index: 1, State: "complete", RetainedOffsetMS: 0}, {Index: 2, State: "complete", RetainedOffsetMS: 2000}}
	if err = meeting.SaveRun(session, run); err != nil {
		t.Fatal(err)
	}
	timeline := []DeskClip{{Index: 1, RetainedOffsetMS: 0, DurationMS: 2000, StartedAt: "2026-11-02T11:00:00+08:00"}, {Index: 2, RetainedOffsetMS: 2000, DurationMS: 2000, StartedAt: "2026-11-02T11:45:00+08:00"}}
	if err = meeting.WriteJSON(filepath.Join(session, "clips.json"), timeline); err != nil {
		t.Fatal(err)
	}
	dir, _ := meeting.RunDir(session, run.ID)
	for i := range run.Clips {
		work := filepath.Join(dir, "clips", []string{"000001", "000002"}[i])
		os.MkdirAll(work, 0700)
		var result meeting.Result
		result.Result.Utterances = []meeting.Utterance{{Start: 500, End: 1500, Text: "sample", Additions: meeting.Speaker{Speaker: "speaker"}}}
		if err = meeting.WriteJSON(filepath.Join(work, "transcript.json"), result); err != nil {
			t.Fatal(err)
		}
	}
	d := Desktop{settings: Settings{Root: store.Root}}
	if err = d.aggregateAutomatic(transcriptionJob{Root: store.Root, SessionID: item.ID, RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	var result meeting.Result
	b, _ := os.ReadFile(filepath.Join(dir, "transcript.json"))
	if err = json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Result.Utterances) != 2 || result.Result.Utterances[1].Start != 2500 || result.Result.Utterances[1].RecordedAt != "2026-11-02T11:45:00.5+08:00" {
		t.Fatal("aggregate discarded clock metadata")
	}
	if err = writeSpeakerTranscript(dir, Settings{}, map[string]string{"speaker": "Alice"}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "transcript.md"))
	if !strings.Contains(string(b), "**[2026-11-02 11:45:00] Alice**") {
		t.Fatal("speaker correction lost wall clock")
	}
}

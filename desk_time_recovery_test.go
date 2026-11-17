package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mettings.local/desktop/internal/desk"
	"mettings.local/desktop/internal/meeting"
)

func mustDeskTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestDeskCaptureAnchorsPreserveGapInsideClip(t *testing.T) {
	clips := []DeskClip{{Index: 7, RetainedOffsetMS: 10000, DurationMS: 10000, StartedAt: "2026-11-06T10:00:00+08:00", TimestampSource: "capture-pts", TimeAnchors: []DeskTimeAnchor{{OffsetMS: 0, RecordedAt: "2026-11-06T10:00:00+08:00"}, {OffsetMS: 4000, RecordedAt: "2026-11-06T15:00:00+08:00"}}}}
	var result meeting.Result
	result.Result.Utterances = []meeting.Utterance{{Start: 13500, End: 13900}, {Start: 14500, End: 15500}}
	stampDeskResult(&result, clips, -1)
	if result.Result.Utterances[0].RecordedAt != "2026-11-06T10:00:03.5+08:00" || result.Result.Utterances[1].RecordedAt != "2026-11-06T15:00:00.5+08:00" {
		t.Fatalf("capture gap flattened: %+v", result.Result.Utterances)
	}
	if result.Result.Utterances[1].Start != 14500 || result.Result.Utterances[1].RecordedAtEstimated {
		t.Fatal("audio offset or capture provenance changed")
	}
	local := meeting.Result{}
	local.Result.Utterances = []meeting.Utterance{{Start: 4500, End: 5500}}
	stampDeskResult(&local, clips, 7)
	if local.Result.Utterances[0].RecordedAt != result.Result.Utterances[1].RecordedAt {
		t.Fatal("clip-local timestamp differs from whole-session timestamp")
	}
}

func TestHistoricalDeskRecoveryPreservesOrderAcrossQueuedClipWrites(t *testing.T) {
	clips := []DeskClip{{Index: 1, StartMS: 0, EndMS: 30000, RetainedOffsetMS: 0, DurationMS: 30000, StartedAt: "2026-11-06T09:00:00+08:00"}, {Index: 2, StartMS: 30000, EndMS: 32000, RetainedOffsetMS: 30000, DurationMS: 2000, StartedAt: "2026-11-06T09:00:30+08:00"}, {Index: 3, StartMS: 120000, EndMS: 123000, RetainedOffsetMS: 32000, DurationMS: 3000, StartedAt: "2026-11-06T09:02:00+08:00"}}
	seals := map[int]time.Time{1: mustDeskTime(t, "2026-11-06T15:00:30+08:00"), 2: mustDeskTime(t, "2026-11-06T15:00:30.1+08:00"), 3: mustDeskTime(t, "2026-11-06T15:05:00+08:00")}
	fixed, estimated, err := recoverDeskTimeline(clips, seals)
	if err != nil || !estimated {
		t.Fatal("historical drift not recovered", err)
	}
	for i, c := range fixed {
		start := mustDeskTime(t, c.StartedAt)
		if start.Add(time.Duration(c.DurationMS) * time.Millisecond).After(seals[c.Index]) {
			t.Fatal("estimated capture ends after clip was sealed")
		}
		if c.TimestampSource != deskEstimatedTimestampSource || c.RetainedOffsetMS != clips[i].RetainedOffsetMS || c.StartMS != clips[i].StartMS {
			t.Fatal("recovery changed audio offsets")
		}
		if i > 0 {
			prev := fixed[i-1]
			if start.Before(mustDeskTime(t, prev.StartedAt).Add(time.Duration(prev.DurationMS) * time.Millisecond)) {
				t.Fatal("queued writes made historical time run backwards")
			}
		}
	}
	if clips[0].StartedAt != "2026-11-06T09:00:00+08:00" {
		t.Fatal("recovery changed source manifest values")
	}
	if fixed[2].StartedAt != "2026-11-06T15:04:57+08:00" {
		t.Fatal("final historical clip remains hours early")
	}
	var result meeting.Result
	result.Result.Utterances = []meeting.Utterance{{Start: 32000, End: 33000, Text: "fixture"}}
	stampDeskResult(&result, fixed, -1)
	md := meeting.Markdown(result)
	if !result.Result.Utterances[0].RecordedAtEstimated || !strings.Contains(md, meeting.EstimatedTimestampNotice) || !strings.Contains(md, "2026-11-06 15:04:57") {
		t.Fatal("export presents estimated date as exact")
	}
}

func TestHistoricalDeskRecoveryLeavesNormalLatencyAndCapturePTSAlone(t *testing.T) {
	for _, source := range []string{"", "capture-pts"} {
		clips := []DeskClip{{Index: 1, DurationMS: 1000, StartedAt: "2026-11-06T10:00:00+08:00", TimestampSource: source}}
		seal := "2026-11-06T10:00:04+08:00"
		if source != "" {
			seal = "2026-11-06T15:00:04+08:00"
		}
		fixed, estimated, err := recoverDeskTimeline(clips, map[int]time.Time{1: mustDeskTime(t, seal)})
		if err != nil || estimated || fixed[0].StartedAt != clips[0].StartedAt {
			t.Fatal("normal or timestamped capture was reinterpreted")
		}
	}
}

func TestHistoricalCalendarMarkdownCanBeCorrectedReadOnly(t *testing.T) {
	clips := []DeskClip{{Index: 1, RetainedOffsetMS: 0, DurationMS: 1000, StartedAt: "2026-11-06T15:00:00+08:00", TimestampSource: deskEstimatedTimestampSource}}
	md := "# 转写稿\n\n**[2026-11-06 10:00:00] Alice**：fixture"
	fixed := datedDeskMarkdown(md, clips, []meeting.Utterance{{Start: 0}})
	if !strings.Contains(fixed, "[2026-11-06 15:00:00] Alice") || strings.Contains(fixed, "10:00:00") {
		t.Fatal("persisted wrong calendar date survived display correction")
	}
}

func TestReadDeskTimelineRecoveryDoesNotRewriteManifestOrAudio(t *testing.T) {
	if _, ok := deskClipSealTime(fileInfoForRecoveryTest(t)); !ok {
		t.Skip("platform cannot attest native clip creation times")
	}
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "clips"), 0700)
	path := filepath.Join(dir, "clips", "000001.wav")
	if _, _, err := desk.Append(path, make([]byte, 32000)); err != nil {
		t.Fatal(err)
	}
	clip := DeskClip{Index: 1, Path: "clips/000001.wav", DurationMS: 1000, StartedAt: time.Now().Add(-2 * time.Hour).Format(time.RFC3339Nano)}
	if err := meeting.WriteJSON(filepath.Join(dir, "clips.json"), []DeskClip{clip}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "clips.json"))
	audio, _ := os.ReadFile(path)
	fixed, estimated, err := readDeskDisplayTimeline(dir)
	if err != nil || !estimated || len(fixed) != 1 {
		t.Fatal("read-only recovery missing", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "clips.json"))
	audioAfter, _ := os.ReadFile(path)
	if string(before) != string(after) || string(audio) != string(audioAfter) {
		t.Fatal("display recovery rewrote saved user data")
	}
	var stored []DeskClip
	json.Unmarshal(after, &stored)
	if stored[0].TimestampSource != "" {
		t.Fatal("estimated source persisted into original manifest")
	}
}

func fileInfoForRecoveryTest(t *testing.T) os.FileInfo {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(p, nil, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"mettings.local/desktop/internal/meeting"
)

const deskEstimatedTimestampSource = "estimated-file"
const deskHistoricalClockDrift = 30 * time.Second

func readDeskTimeline(sessionDir string) ([]DeskClip, error) {
	clips, _, err := readDeskDisplayTimeline(sessionDir)
	return clips, err
}

// Old builds persisted sample-count clocks without capture timestamps. Recover
// display/rerun estimates from sealed local clips; never rewrite the manifest.
func readDeskDisplayTimeline(sessionDir string) ([]DeskClip, bool, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, "clips.json"))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var clips []DeskClip
	if err = json.Unmarshal(data, &clips); err != nil {
		return nil, false, err
	}
	seals := map[int]time.Time{}
	for _, clip := range clips {
		if clip.TimestampSource != "" || !filepath.IsLocal(clip.Path) || clip.DurationMS <= 0 {
			continue
		}
		info, e := os.Stat(filepath.Join(sessionDir, clip.Path))
		// The native writer emits a fixed 44-byte PCM16/mono/16 kHz header. A
		// changed/truncated file cannot supply a trustworthy saved-clip estimate.
		if e != nil || info.Size() < 44 || (info.Size()-44)/32 != clip.DurationMS {
			continue
		}
		if at, ok := deskClipSealTime(info); ok {
			seals[clip.Index] = at
		}
	}
	return recoverDeskTimeline(clips, seals)
}

// Seal times can lag capture when several clips flush together. A suffix-minimum
// drift bound removes transient write queues while preserving original sample
// order/minimum gaps. It cannot restore capture PTS which old builds discarded.
func recoverDeskTimeline(clips []DeskClip, seals map[int]time.Time) ([]DeskClip, bool, error) {
	recovered := append([]DeskClip(nil), clips...)
	estimated := false
	var upper time.Duration
	haveUpper := false
	for i := len(recovered) - 1; i >= 0; i-- {
		clip := &recovered[i]
		if clip.TimestampSource != "" {
			estimated = estimated || clip.TimestampSource == deskEstimatedTimestampSource
			haveUpper = false
			continue
		}
		at, e := time.Parse(time.RFC3339Nano, clip.StartedAt)
		seal, exists := seals[clip.Index]
		if e != nil || !exists || clip.DurationMS <= 0 {
			continue
		}
		drift := seal.Sub(at.Add(time.Duration(clip.DurationMS) * time.Millisecond))
		if !haveUpper || drift < upper {
			upper, haveUpper = drift, true
		}
		// Small VAD/flush latency is normal. Do not reinterpret an accurate legacy
		// timestamp merely because the file was sealed a few seconds later.
		if upper < deskHistoricalClockDrift {
			continue
		}
		clip.StartedAt = at.Add(upper).Format(time.RFC3339Nano)
		clip.TimestampSource = deskEstimatedTimestampSource
		clip.TimeAnchors = nil
		estimated = true
	}
	return recovered, estimated, nil
}

// Retained offsets are used for playback/extraction. Piecewise capture anchors
// preserve missing-input intervals, including gaps inside one saved voice clip.
func deskRecordedAtInfo(clips []DeskClip, retainedMS int64) (string, bool) {
	for _, clip := range clips {
		if retainedMS < clip.RetainedOffsetMS || retainedMS >= clip.RetainedOffsetMS+clip.DurationMS {
			continue
		}
		offset := retainedMS - clip.RetainedOffsetMS
		at, err := time.Parse(time.RFC3339Nano, clip.StartedAt)
		if err != nil {
			return "", false
		}
		anchorOffset := int64(0)
		for _, anchor := range clip.TimeAnchors {
			if anchor.OffsetMS < 0 || anchor.OffsetMS > offset || anchor.OffsetMS < anchorOffset {
				continue
			}
			if parsed, e := time.Parse(time.RFC3339Nano, anchor.RecordedAt); e == nil {
				at, anchorOffset = parsed, anchor.OffsetMS
			}
		}
		return at.Add(time.Duration(offset-anchorOffset) * time.Millisecond).Format(time.RFC3339Nano), clip.TimestampSource == deskEstimatedTimestampSource
	}
	return "", false
}
func deskRecordedAt(clips []DeskClip, retainedMS int64) string {
	at, _ := deskRecordedAtInfo(clips, retainedMS)
	return at
}

func stampDeskResult(result *meeting.Result, clips []DeskClip, clipIndex int) {
	if len(clips) == 0 {
		return
	}
	offset := int64(0)
	if clipIndex >= 0 {
		found := false
		for _, clip := range clips {
			if clip.Index == clipIndex {
				offset = clip.RetainedOffsetMS
				found = true
				break
			}
		}
		if !found {
			return
		}
	}
	for i := range result.Result.Utterances {
		if at, estimated := deskRecordedAtInfo(clips, result.Result.Utterances[i].Start+offset); at != "" {
			result.Result.Utterances[i].RecordedAt = at
			result.Result.Utterances[i].RecordedAtEstimated = estimated
		}
	}
}

var deskDisplayTimestamp = regexp.MustCompile(`\*\*\[(?:\d{4}-\d{2}-\d{2} )?\d+:\d{2}:\d{2}\]`)

// Use canonical audio offsets to replace both old relative markers and calendar
// dates persisted by the broken first-anchor clock. Display changes are read-only.
func datedDeskMarkdown(md string, clips []DeskClip, utterances []meeting.Utterance) string {
	if len(deskDisplayTimestamp.FindAllStringIndex(md, -1)) != len(utterances) {
		return md
	}
	index := 0
	return deskDisplayTimestamp.ReplaceAllStringFunc(md, func(marker string) string {
		offset := utterances[index].Start
		index++
		at, err := time.Parse(time.RFC3339Nano, deskRecordedAt(clips, offset))
		if err != nil {
			return marker
		}
		return "**[" + at.Format("2006-01-02 15:04:05") + "]"
	})
}

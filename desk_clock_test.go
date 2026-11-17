package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeskSourceTimeKeepsCaptureGap(t *testing.T) {
	start := time.Date(2026, 11, 5, 10, 0, 0, 0, time.Local)
	r := deskCapture{started: start}
	r.sourceStarted.Store(start.UnixNano())
	// A microphone presents one second of PCM, stops for four hours, then
	// resumes. The resumed samples' date must come from capture timestamps.
	r.captureClock = newDeskCaptureClock(start)
	r.captureClock.addFrame(start.Unix()*16000, 16000, 16000)
	resume := start.Add(4*time.Hour + time.Second)
	r.captureClock.addFrame(resume.Unix()*16000, 16000, 16000)
	if got := r.sourceTime(1500); !got.Equal(resume.Add(500 * time.Millisecond)) {
		t.Fatalf("resumed voice time=%s, want %s", got, resume.Add(500*time.Millisecond))
	}
	if got := r.sourceTime(500); !got.Equal(start.Add(500 * time.Millisecond)) {
		t.Fatalf("earlier voice changed after resume: %s", got)
	}
}

func TestDeskClockExactPTSAndClipAnchors(t *testing.T) {
	start := time.Date(2026, 11, 5, 10, 0, 0, 123456789, time.Local)
	clock := newDeskCaptureClock(start)
	writer := deskTimestampWriter{clock: clock}
	pts := start.Unix()*16000 + 1975 // 123437500ns is exact at 16kHz.
	line := fmt.Sprintf("[Parsed_ashowinfo_1] n:0 pts:%d pts_time:1.79e+09 fmt:flt channels:1 rate:16000 nb_samples:16000\n", pts)
	writer.Write([]byte(line[:15]))
	writer.Write([]byte(line[15:]))
	clock.addFrame(pts+16000+4*3600*16000, 16000, 16000)
	anchors := clock.clipAnchors(500, 2000)
	if len(anchors) != 2 || anchors[0].OffsetMS != 0 || anchors[1].OffsetMS != 500 {
		t.Fatalf("missing intra-clip capture gap: %+v", anchors)
	}
	first, _ := time.Parse(time.RFC3339Nano, anchors[0].RecordedAt)
	expected := time.Unix(pts/16000, (pts%16000)*int64(time.Second)/16000).Add(500 * time.Millisecond)
	if !first.Equal(expected) {
		t.Fatalf("rounded pts_time was used: %s vs %s", first, expected)
	}
	// A later small delivery jitter must not rewrite already emitted anchors.
	clock.addFrame(pts+32000+4*3600*16000+800, 16000, 16000)
	if len(clock.anchors) != 2 {
		t.Fatalf("small frame jitter bloated clock: %d", len(clock.anchors))
	}
}

func TestDeskClockRejectsRelativeOrInvalidMetadata(t *testing.T) {
	start := time.Date(2026, 11, 5, 10, 0, 0, 0, time.Local)
	for _, line := range []string{
		"[Parsed_ashowinfo_1] pts:0 pts_time:0 rate:16000 nb_samples:512\n",
		"[Parsed_ashowinfo_1] pts:NOPTS pts_time:NOPTS rate:16000 nb_samples:512\n",
		fmt.Sprintf("[Parsed_ashowinfo_1] pts:%d pts_time:epoch rate:48000 nb_samples:512\n", start.Unix()*48000),
	} {
		clock := newDeskCaptureClock(start)
		writer := deskTimestampWriter{clock: clock}
		writer.Write([]byte(line))
		if clock.failure() == nil {
			t.Fatal("accepted invalid/relative capture clock")
		}
		r := deskCapture{started: start, captureClock: clock}
		r.sourceStarted.Store(start.UnixNano())
		if !r.sourceTime(0).IsZero() {
			t.Fatal("invalid capture timestamps silently fell back to first-read time")
		}
	}
}

// Real FFmpeg emits exact filter PTS on stderr while raw PCM deliberately
// contains no four-hour silence. The transport must preserve both timelines.
func TestDeskClockRealFFmpegPreservesCaptureDiscontinuity(t *testing.T) {
	fixtures := os.Getenv("METTINGS_NATIVE_FIXTURES")
	if fixtures == "" {
		t.Skip("set METTINGS_NATIVE_FIXTURES for real FFmpeg")
	}
	start := time.Date(2026, 11, 5, 10, 0, 0, 0, time.Local)
	clock := newDeskCaptureClock(start)
	filter := fmt.Sprintf("aresample=16000,asetpts=PTS+%d/TB+if(gte(T\\,1)\\,14400/TB\\,0),ashowinfo", start.Unix())
	cmd := exec.Command(filepath.Join(fixtures, "ffmpeg", "ffmpeg"), "-hide_banner", "-loglevel", "info", "-nostats", "-copyts", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2", "-af", filter, "-ac", "1", "-ar", "16000", "-f", "f32le", "pipe:1")
	cmd.Stderr = &deskTimestampWriter{clock: clock}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var initial atomic.Int64
	reader := sourceClockReader{source: output, clock: &initial, timing: clock}
	n, readErr := io.Copy(io.Discard, &reader)
	waitErr := cmd.Wait()
	if readErr != nil || waitErr != nil {
		t.Fatalf("capture transport failed: read=%v wait=%v clock=%v", readErr, waitErr, clock.failure())
	}
	if n != 2*16000*4 || clock.samples != 32000 {
		t.Fatalf("PCM and timing diverged: bytes=%d sample metadata=%d", n, clock.samples)
	}
	got, ok := clock.timeAt(1500)
	if !ok || got.Before(start.Add(4*time.Hour+1400*time.Millisecond)) || got.After(start.Add(4*time.Hour+1600*time.Millisecond)) {
		t.Fatalf("capture gap lost after resampling: %s", got)
	}
}

func TestDeskClockMetadataFailureDoesNotForwardUnstampedPCM(t *testing.T) {
	start := time.Date(2026, 11, 5, 10, 0, 0, 0, time.Local)
	clock := newDeskCaptureClock(start)
	clock.addFrame(0, 16000, 512)
	var initial atomic.Int64
	reader := sourceClockReader{source: bytes.NewReader(make([]byte, 512*4)), clock: &initial, timing: clock}
	buffer := make([]byte, 512*4)
	n, err := reader.Read(buffer)
	if n != 0 || err == nil || initial.Load() != 0 {
		t.Fatalf("unstamped PCM forwarded: n=%d err=%v fallback=%d", n, err, initial.Load())
	}
}

func TestDeskClockTracksGradualCaptureDrops(t *testing.T) {
	start := time.Date(2026, 11, 5, 10, 0, 0, 0, time.Local)
	clock := newDeskCaptureClock(start)
	for frame := int64(0); frame < 1000; frame++ {
		// Device drops remove 200ms for every second of retained raw PCM.
		at := start.Add(time.Duration(frame) * 1200 * time.Millisecond)
		pts := at.Unix()*16000 + int64(at.Nanosecond())*16000/int64(time.Second)
		clock.addFrame(pts, 16000, 16000)
	}
	got, ok := clock.timeAt(999500)
	want := start.Add(999*1200*time.Millisecond + 500*time.Millisecond)
	if !ok || !got.Equal(want) {
		t.Fatalf("gradual losses accumulated into display drift: got=%s want=%s", got, want)
	}
}

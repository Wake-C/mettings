package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mettings.local/desktop/internal/desk"
	"mettings.local/desktop/internal/meeting"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newDeskCoordinatorFixture(t *testing.T, auto bool) (*Desktop, *deskCapture, meeting.Store) {
	t.Helper()
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	s := defaultSettings()
	s.Root = t.TempDir()
	s.DeskAutoASR = auto
	r := &deskCapture{settings: s, started: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC), spool: t.TempDir(), done: make(chan error, 1)}
	d := &Desktop{settings: s, desk: r, deskState: DeskState{Active: true}}
	return d, r, meeting.Store{Root: s.Root}
}
func coordinatorClip(t *testing.T, r *deskCapture, name string, start int64, marker byte) captureEvent {
	t.Helper()
	path := filepath.Join(r.spool, name)
	if _, _, err := desk.Append(path, bytes.Repeat([]byte{marker, 0}, 16000)); err != nil {
		t.Fatal(err)
	}
	return captureEvent{Type: "clip", Path: path, StartMS: start, EndMS: start + 1000}
}
func TestDeskCoordinatorClocksGroupingAndAudioOnly(t *testing.T) {
	d, r, store := newDeskCoordinatorFixture(t, false)
	for _, e := range []captureEvent{coordinatorClip(t, r, "a.wav", 10000, 1), coordinatorClip(t, r, "b.wav", 60000, 2)} {
		if err := d.handleDeskEvent(r, e); err != nil {
			t.Fatal(err)
		}
	}
	id := r.sessionID
	item, err := store.Find(id)
	if err != nil {
		t.Fatal(err)
	}
	if !item.Active || item.Mode != "desk" || item.ClipCount != 2 || item.SpeechMS != 2000 || item.ElapsedMS != 51000 {
		t.Fatalf("wrong conversation state %+v", item)
	}
	dir := filepath.Join(store.Root, item.Directory)
	pcm, err := desk.PCM16(filepath.Join(dir, item.Audio))
	if err != nil || !bytes.Equal(pcm, append(bytes.Repeat([]byte{1, 0}, 16000), bytes.Repeat([]byte{2, 0}, 16000)...)) {
		t.Fatal("retained audio changed", err)
	}
	var clips []DeskClip
	b, err := os.ReadFile(filepath.Join(dir, "clips.json"))
	if err != nil || json.Unmarshal(b, &clips) != nil || len(clips) != 2 {
		t.Fatal("clip index missing", err)
	}
	if clips[0].StartMS != 10000 || clips[1].StartMS != 60000 || clips[1].RetainedOffsetMS != 1000 || clips[1].StartedAt != "2026-10-29T09:01:00Z" {
		t.Fatalf("source/retained clocks mixed %+v", clips)
	}
	for _, at := range []int64{151000, 42 * 60 * 1000, 26 * 60 * 60 * 1000} {
		if err = d.handleDeskEvent(r, captureEvent{Type: "activity", AtMS: at, Speech: false}); err != nil {
			t.Fatal(err)
		}
		if r.sessionID != id {
			t.Fatal("quiet interval closed the Desk run")
		}
	}
	if err = d.handleDeskEvent(r, coordinatorClip(t, r, "c.wav", 26*60*60*1000+1000, 3)); err != nil {
		t.Fatal(err)
	}
	if r.sessionID != id {
		t.Fatal("later speech created another folder within the same Desk run")
	}
	items, err := store.List()
	if err != nil || len(items) != 1 || items[0].ClipCount != 3 || items[0].SpeechMS != 3000 || !items[0].Active {
		t.Fatalf("expected one active append-only folder: %+v %v", items, err)
	}
	pcm, err = desk.PCM16(filepath.Join(dir, item.Audio))
	want := append(append(bytes.Repeat([]byte{1, 0}, 16000), bytes.Repeat([]byte{2, 0}, 16000)...), bytes.Repeat([]byte{3, 0}, 16000)...)
	if err != nil || !bytes.Equal(pcm, want) {
		t.Fatal("speech after quiet was lost or replaced earlier audio", err)
	}
	b, err = os.ReadFile(filepath.Join(dir, "clips.json"))
	if err != nil || json.Unmarshal(b, &clips) != nil || len(clips) != 3 || clips[2].RetainedOffsetMS != 2000 || clips[2].StartedAt != "2026-10-30T11:00:01Z" {
		t.Fatalf("later clip wall-clock or playback offset lost: %+v %v", clips, err)
	}
	if err = d.finishDeskConversation(r, nil); err != nil {
		t.Fatal(err)
	}
	item, err = store.Find(id)
	if err != nil || item.Active || r.sessionID != "" {
		t.Fatal("explicit stop did not close the run", err)
	}
	if state := d.TranscriptionStatus(); state.Running || state.Queued != 0 {
		t.Fatal("audio-only capture scheduled ASR")
	}
	// A fresh Start after Pause owns a new folder; silence within a run does not.
	next := &deskCapture{settings: r.settings, started: r.started.Add(27 * time.Hour), spool: t.TempDir()}
	if err = d.handleDeskEvent(next, coordinatorClip(t, next, "next.wav", 1000, 4)); err != nil {
		t.Fatal(err)
	}
	items, err = store.List()
	if err != nil || len(items) != 2 || next.sessionID == id {
		t.Fatalf("fresh Desk run did not get its own folder: %+v %v", items, err)
	}
}
func TestDeskCoordinatorSilenceCreatesNoSession(t *testing.T) {
	d, r, store := newDeskCoordinatorFixture(t, false)
	for _, at := range []int64{1000, 90000, 8 * 60 * 60 * 1000} {
		if err := d.handleDeskEvent(r, captureEvent{Type: "activity", AtMS: at}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.List()
	if err != nil || len(items) != 0 || r.sessionID != "" {
		t.Fatalf("idle noise created a session %+v %v", items, err)
	}
}
func TestDeskCoordinatorRetainsClipsWhileASRBlocked(t *testing.T) {
	d, r, store := newDeskCoordinatorFixture(t, true)
	started, release := make(chan struct{}), make(chan struct{})
	calls := 0
	q := d.transcriptionQueue()
	q.execute = func(context.Context, transcriptionJob) error {
		calls++
		if calls == 1 {
			close(started)
			<-release
		}
		return nil
	}
	defer func() {
		close(release)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := d.WaitTranscriptions(ctx); err != nil {
			t.Error(err)
		}
	}()
	if err := d.handleDeskEvent(r, coordinatorClip(t, r, "a.wav", 1000, 1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("ASR worker did not start")
	}
	second := make(chan error, 1)
	secondClip := coordinatorClip(t, r, "b.wav", 600000, 2)
	go func() { second <- d.handleDeskEvent(r, secondClip) }()
	select {
	case err := <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("ASR blocked continued capture")
	}
	item, err := store.Find(r.sessionID)
	if err != nil || item.ClipCount != 2 || item.SpeechMS != 2000 || !item.Active {
		t.Fatalf("new clip not persisted while ASR blocked: %+v %v", item, err)
	}
	if state := d.TranscriptionStatus(); !state.Running || state.Queued != 1 {
		t.Fatalf("wrong independent queue state %+v", state)
	}
}
func TestDeskCoordinatorClipErrorPreservesEarlierAudio(t *testing.T) {
	d, r, store := newDeskCoordinatorFixture(t, false)
	if err := d.handleDeskEvent(r, coordinatorClip(t, r, "good.wav", 1000, 7)); err != nil {
		t.Fatal(err)
	}
	id := r.sessionID
	item, _ := store.Find(id)
	path := filepath.Join(store.Root, item.Directory, item.Audio)
	before, _ := os.ReadFile(path)
	bad := filepath.Join(r.spool, "bad.wav")
	os.WriteFile(bad, []byte("broken audio"), 0600)
	if err := d.handleDeskEvent(r, captureEvent{Type: "clip", Path: bad, StartMS: 5000, EndMS: 6000}); err == nil {
		t.Fatal("malformed clip accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("earlier recording damaged")
	}
	if _, err := os.Stat(bad); err != nil {
		t.Fatal("failed source clip discarded")
	}
	if err := recoverCaptures(store.Root); err != nil {
		t.Fatal(err)
	}
	item, _ = store.Find(id)
	if item.Active || item.Error == "" {
		t.Fatal("interrupted capture not recovered")
	}
	if _, err := desk.PCM16(path); err != nil {
		t.Fatal("recovery damaged playable audio")
	}
}
func TestDeskCoordinatorStoppedCaptureFailureKeepsSpool(t *testing.T) {
	d, r, _ := newDeskCoordinatorFixture(t, false)
	orphan := coordinatorClip(t, r, "sealed-unimported.wav", 1000, 9).Path
	r.capture = exec.Command("/bin/sh", "-c", "exit 7")
	r.detector = exec.Command("/bin/sh", "-c", "exit 0")
	var err error
	r.input, err = r.capture.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	r.pcm = io.NopCloser(strings.NewReader(""))
	if err = r.capture.Start(); err != nil {
		t.Fatal(err)
	}
	if err = r.detector.Start(); err != nil {
		t.Fatal(err)
	}
	r.stopping.Store(true)
	d.runDesk(r, io.NopCloser(strings.NewReader("")))
	if err = <-r.done; err == nil {
		t.Error("nonzero capture exit during StopDesk reported success")
	}
	if _, err = os.Stat(orphan); err != nil {
		t.Error("capture failure removed the unimported sealed clip")
	}
}

func TestDeskCoordinatorUnexpectedDetectorEOFUnblocksPCMReader(t *testing.T) {
	d, r, _ := newDeskCoordinatorFixture(t, false)
	// sleep stands in for a microphone process awaiting permission: its stdout
	// stays open but produces no samples. The wrapped reader matches timestamping
	// readers and makes os/exec manage a stdin-copy goroutine for the detector.
	r.capture = exec.Command("/bin/sleep", "30")
	var err error
	r.input, err = r.capture.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	r.pcm, err = r.capture.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	r.detector = exec.Command("/usr/bin/true")
	r.detector.Stdin = &sourceClockReader{source: r.pcm, clock: &r.sourceStarted}
	events, err := r.detector.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = r.detector.Start(); err != nil {
		t.Fatal(err)
	}
	if err = r.capture.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.capture.Process.Kill()
	defer r.detector.Process.Kill()
	go d.runDesk(r, events)
	select {
	case err = <-r.done:
		if err == nil {
			t.Error("unexpected detector termination was reported as success")
		}
	case <-time.After(time.Second):
		t.Error("detector EOF did not stop upstream; Wait blocked on the PCM-copy reader")
		_ = r.capture.Process.Kill()
		_ = r.pcm.Close()
		select {
		case <-r.done:
		case <-time.After(time.Second):
			t.Error("coordinator did not finish after explicit pipe cleanup")
		}
	}
}

func TestDeskCoordinatorPermissionDelayUsesFirstPCMClock(t *testing.T) {
	d, r, store := newDeskCoordinatorFixture(t, false)
	sourceStart := r.started.Add(5*time.Minute + 123456789*time.Nanosecond)
	r.sourceStarted.Store(sourceStart.UnixNano())
	if err := d.handleDeskEvent(r, coordinatorClip(t, r, "after-permission.wav", 10000, 4)); err != nil {
		t.Fatal(err)
	}
	item, err := store.Find(r.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := sourceStart.Add(10 * time.Second)
	gotTime, parseErr := time.Parse(time.RFC3339Nano, item.StartedAt)
	if parseErr != nil || !gotTime.Equal(want) || !strings.HasPrefix(item.Directory, want.In(time.Local).Format("2006-01-02-150405")) {
		t.Fatalf("permission delay shifted session clock: %+v", item)
	}
	var clips []DeskClip
	b, err := os.ReadFile(filepath.Join(store.Root, item.Directory, "clips.json"))
	if err != nil || json.Unmarshal(b, &clips) != nil || len(clips) != 1 {
		t.Fatal("clip index missing")
	}
	clipTime, parseErr := time.Parse(time.RFC3339Nano, clips[0].StartedAt)
	if parseErr != nil || !clipTime.Equal(want) || clips[0].StartMS != 10000 {
		t.Fatalf("source offset/clock mismatch %+v", clips[0])
	}
}

func TestNativeDeskCoordinatorStopFlushesAndArchivesWithoutASR(t *testing.T) {
	fixtures := os.Getenv("METTINGS_NATIVE_FIXTURES")
	if fixtures == "" {
		t.Skip("set METTINGS_NATIVE_FIXTURES for the native coordinator pipe test")
	}
	if _, err := os.Stat(deskHelperPath()); err != nil {
		t.Fatalf("build the bundled desk helper first: %v", err)
	}
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	os.MkdirAll(filepath.Dir(ffmpegPath()), 0700)
	source := filepath.Join(fixtures, "models", "sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2025-09-09", "test_wavs", "zh.wav")
	binary := filepath.Join(fixtures, "ffmpeg", "ffmpeg")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nexec " + quote(binary) + " -hide_banner -loglevel info -nostats -copyts -use_wallclock_as_timestamps 1 -re -stream_loop -1 -i " + quote(source) + " -vn -af aresample=16000,ashowinfo -ac 1 -ar 16000 -f f32le pipe:1\n"
	if err := os.WriteFile(ffmpegPath(), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(runtimeDir(), ".ffmpeg.installed"), []byte(components[0].SHA), 0600)
	model, err := os.ReadFile(filepath.Join(fixtures, "silero_vad.onnx"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(runtimeDir(), "silero_vad.onnx"), model, 0600)
	os.WriteFile(filepath.Join(runtimeDir(), "."+vadComponent.ID+".installed"), []byte(vadComponent.SHA), 0600)
	s := defaultSettings()
	s.Root = t.TempDir()
	s.DeskAutoASR = false
	d := &Desktop{settings: s}
	started := time.Now()
	if err = d.StartDesk(); err != nil {
		t.Fatal(err)
	}
	defer d.StopDesk()
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	detected := false
	for !detected {
		select {
		case <-deadline:
			t.Fatal("native detector did not receive public sample speech")
		case <-ticker.C:
			snap, e := d.Snapshot()
			if e != nil {
				t.Fatal(e)
			}
			if snap.Desk.Error != "" {
				t.Fatal(snap.Desk.Error)
			}
			detected = snap.Desk.Phase == "检测到交谈"
		}
	}
	if err = d.StopDesk(); err != nil {
		t.Fatal(err)
	}
	snap, err := d.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Desk.Active || snap.JobsRunning || snap.Queued != 0 || len(snap.Sessions) != 1 {
		t.Fatalf("unexpected finished state %+v", snap)
	}
	item := snap.Sessions[0]
	if item.Active || item.Mode != "desk" || item.ClipCount == 0 || item.SpeechMS <= 0 || item.Transcribed {
		t.Fatalf("stopped speech not archived as audio-only: %+v", item)
	}
	if _, err = desk.PCM16(filepath.Join(s.Root, item.Directory, item.Audio)); err != nil {
		t.Fatal("final audio not playable", err)
	}
	var clips []DeskClip
	data, err := os.ReadFile(filepath.Join(s.Root, item.Directory, "clips.json"))
	if err != nil || json.Unmarshal(data, &clips) != nil || len(clips) == 0 {
		t.Fatal("capture clip timestamps missing", err)
	}
	for _, clip := range clips {
		at, err := time.Parse(time.RFC3339Nano, clip.StartedAt)
		if err != nil || clip.TimestampSource != "capture-pts" || len(clip.TimeAnchors) == 0 || clip.TimeAnchors[0].OffsetMS != 0 || at.Before(started.Add(-time.Second)) || at.After(time.Now()) {
			t.Fatalf("native capture lost wall-clock provenance: %+v", clip)
		}
	}
	captureSpools, err := os.ReadDir(filepath.Join(dataDir(), "capture"))
	if err != nil || len(captureSpools) != 0 {
		t.Fatalf("normal stop left unimported spool: %v %v", captureSpools, err)
	}
}

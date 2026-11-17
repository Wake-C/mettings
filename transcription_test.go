package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mettings.local/desktop/internal/meeting"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testTranscriptionDesktop(t *testing.T, handler http.HandlerFunc) (*Desktop, meeting.Store, meeting.Session) {
	t.Helper()
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	s := defaultSettings()
	s.Root = t.TempDir()
	s.ASR = meeting.Provider{Kind: "compatible", Endpoint: server.URL, Model: "model-a", ResponseFormat: "verbose_json"}
	d := &Desktop{settings: s}
	q := d.transcriptionQueue()
	q.execute = func(ctx context.Context, job transcriptionJob) error {
		if job.ClipIndex == -2 {
			return d.executeFinalization(ctx, job)
		}
		return d.executeTranscription(ctx, job, &transcriptionCredentials{})
	}
	store := meeting.Store{Root: s.Root}
	item, err := store.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, item.Directory, item.Audio), []byte("original recording"), 0600); err != nil {
		t.Fatal(err)
	}
	return d, store, item
}
func waitTranscriptTests(t *testing.T, d *Desktop) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.WaitTranscriptions(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestRerunFailureKeepsSuccessfulVersionAndFrozenRetryModel(t *testing.T) {
	var mu sync.Mutex
	models := []string{}
	calls := 0
	d, store, item := testTranscriptionDesktop(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(1 << 20)
		mu.Lock()
		models = append(models, r.FormValue("model"))
		calls++
		call := calls
		mu.Unlock()
		if call == 2 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"text":"result %d"}`, call)
	})
	if err := d.Transcribe(item.ID, "configured"); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	first, err := store.Find(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstDetail, err := d.TranscriptDetail(item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.ActiveRun == "" || !strings.Contains(firstDetail.Transcript, "result 1") {
		t.Fatal("first successful attempt not selected")
	}
	d.mu.Lock()
	d.settings.ASR.Model = "model-b"
	d.mu.Unlock()
	if err = d.Transcribe(item.ID, "configured"); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	failed, err := store.Find(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.ActiveRun != first.ActiveRun || failed.Directory != first.Directory {
		t.Fatal("failed rerun replaced successful result or renamed folder")
	}
	versions, err := d.TranscriptVersions(item.ID)
	if err != nil || len(versions) != 2 || versions[0].State != "failed" {
		t.Fatalf("failed version missing: %+v %v", versions, err)
	}
	if versions[0].Settings != nil {
		t.Fatal("UI response exposed frozen settings")
	}
	lastFailed := versions[0].ID
	d.mu.Lock()
	d.settings.ASR.Model = "model-c"
	d.mu.Unlock()
	if err = d.RetryTranscription(item.ID); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	versions, _ = d.TranscriptVersions(item.ID)
	if len(versions) != 2 || versions[0].ID != lastFailed || versions[0].State != "complete" {
		t.Fatal("retry did not resume same attempt")
	}
	if strings.Join(models, ",") != "model-a,model-b,model-b" {
		t.Fatalf("retry changed its frozen model: %v", models)
	}
	old, _ := d.TranscriptDetail(item.ID, first.ActiveRun)
	if old.Transcript != firstDetail.Transcript {
		t.Fatal("previous version changed")
	}
	updated, _ := store.Find(item.ID)
	if updated.Directory != first.Directory {
		t.Fatal("rerun renamed an already titled session")
	}
	audio, _ := os.ReadFile(filepath.Join(store.Root, updated.Directory, updated.Audio))
	if string(audio) != "original recording" {
		t.Fatal("original audio altered")
	}
}
func TestLegacyTranscriptIsKeptAsVersion(t *testing.T) {
	d, store, item := testTranscriptionDesktop(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"text":"new result"}`)) })
	dir := filepath.Join(store.Root, item.Directory)
	os.WriteFile(filepath.Join(dir, "transcript.md"), []byte("# Original\nPersonal corrections retained"), 0600)
	item.Transcribed = true
	item.Polished = true
	item.Summarized = true
	store.Save(item)
	if err := d.Transcribe(item.ID, "configured"); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	versions, err := d.TranscriptVersions(item.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("missing legacy version %v %v", versions, err)
	}
	for _, run := range versions {
		if run.Choice == "legacy" {
			detail, e := d.TranscriptDetail(item.ID, run.ID)
			if e != nil || !strings.Contains(detail.Transcript, "Personal corrections retained") {
				t.Fatal("legacy text not preserved")
			}
			return
		}
	}
	t.Fatal("no legacy run")
}
func installFakeClipConverter(t *testing.T) {
	t.Helper()
	os.MkdirAll(filepath.Dir(ffmpegPath()), 0700)
	script := "#!/bin/sh\nfor target do :; done\nprintf fixture > \"$target\"\n"
	if err := os.WriteFile(ffmpegPath(), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
}
func TestDeskAppendsOnlyNewClipsAndRetainsCaptureMetadata(t *testing.T) {
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	d, store, item := testTranscriptionDesktop(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			close(firstStarted)
			<-release
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"segments":[{"start":0.2,"end":1.0,"speaker":"1","text":"clip %d"}]}`, n)
	})
	installFakeClipConverter(t)
	item.Mode = "desk"
	item.Active = true
	item.Audio = "recording.wav"
	item.SpeechMS = 1000
	item.ClipCount = 1
	store.Save(item)
	s := d.GetSettings()
	dir := filepath.Join(store.Root, item.Directory)
	clips := []string{}
	for i := 1; i <= 2; i++ {
		p := filepath.Join(dir, fmt.Sprintf("clip%d.wav", i))
		os.WriteFile(p, []byte(fmt.Sprintf("audio%d", i)), 0600)
		clips = append(clips, p)
	}
	if err := d.enqueueDeskClip(item.ID, 1, clips[0], 0, 10000, s); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first clip not started")
	}
	// While ASR is waiting, the capture side can persist another clip.
	d.dataMu.Lock()
	latest, _ := store.Find(item.ID)
	latest.SpeechMS = 2000
	latest.ElapsedMS = 90000
	latest.ClipCount = 2
	store.Save(latest)
	d.dataMu.Unlock()
	if err := d.enqueueDeskClip(item.ID, 2, clips[1], 1000, 80000, s); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitTranscriptTests(t, d)
	current, _ := store.Find(item.ID)
	if current.SpeechMS != 2000 || current.ClipCount != 2 || !current.Active {
		t.Fatal("ASR overwrote active capture metadata")
	}
	if current.Directory != item.Directory {
		t.Fatal("active desk folder renamed")
	}
	versions, _ := d.TranscriptVersions(item.ID)
	if len(versions) != 1 || versions[0].State != "partial" || len(versions[0].Clips) != 2 {
		t.Fatalf("wrong automatic version %+v", versions)
	}
	detail, err := d.TranscriptDetail(item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail.Transcript, "clip 1") || !strings.Contains(detail.Transcript, "clip 2") {
		t.Fatal("incremental text missing")
	}
	runDir, _ := meeting.RunDir(dir, versions[0].ID)
	var result meeting.Result
	b, _ := os.ReadFile(filepath.Join(runDir, "transcript.json"))
	json.Unmarshal(b, &result)
	if len(result.Result.Utterances) != 2 || result.Result.Utterances[1].Start != 1200 || result.Result.Utterances[0].Additions.Speaker == result.Result.Utterances[1].Additions.Speaker {
		t.Fatalf("clip offsets or local speaker scope lost: %+v", result)
	}
	d.dataMu.Lock()
	current.Active = false
	store.Save(current)
	d.dataMu.Unlock()
	if err = d.FinalizeDeskTranscription(item.ID, s); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	versions, _ = d.TranscriptVersions(item.ID)
	if versions[0].State != "complete" || !versions[0].Closed {
		t.Fatal("conversation did not finalize")
	}
	if calls != 2 {
		t.Fatalf("old clips retranscribed: calls=%d", calls)
	}
}
func TestInterruptedRunsRemainRetryable(t *testing.T) {
	store := meeting.Store{Root: t.TempDir()}
	item, _ := store.Create(time.Now())
	dir := filepath.Join(store.Root, item.Directory)
	run, _ := meeting.CreateRun(dir, "test", "configured", json.RawMessage(`{}`), true)
	run.State = "running"
	run.Clips = []meeting.TranscriptClip{{Index: 1, State: "complete"}, {Index: 2, State: "queued"}}
	meeting.SaveRun(dir, run)
	if err := RecoverTranscriptJobs(store.Root); err != nil {
		t.Fatal(err)
	}
	run, _ = meeting.LoadRun(dir, run.ID)
	if run.State != "failed" || run.Clips[0].State != "complete" || run.Clips[1].State != "failed" {
		t.Fatal("interrupted work lost checkpoints")
	}
}

func TestAutomaticAppendKeepsManualSpeakerLabels(t *testing.T) {
	d, store, item := testTranscriptionDesktop(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"segments":[{"start":0,"end":1,"speaker":"1","text":"speech"}]}`))
	})
	installFakeClipConverter(t)
	item.Mode = "desk"
	item.Active = true
	store.Save(item)
	s := d.GetSettings()
	dir := filepath.Join(store.Root, item.Directory)
	clip := filepath.Join(dir, "clip.wav")
	os.WriteFile(clip, []byte("clip"), 0600)
	if err := d.enqueueDeskClip(item.ID, 1, clip, 0, 0, s); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	os.MkdirAll(filepath.Dir(voiceLibrary()), 0700)
	meeting.WriteJSON(voiceLibrary(), VoiceLibrary{Model: voiceModelID, Voices: map[string][]float64{"许宁": {1, 0}}})
	current, _ := store.Find(item.ID)
	runDir, _ := meeting.RunDir(dir, current.ActiveRun)
	if err := labelVoiceWithEmbed(context.Background(), s, runDir, "片段1-1", "许宁", func(context.Context, string) ([]float64, error) { return []float64{1, 0}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := d.enqueueDeskClip(item.ID, 2, clip, 1000, 8000, s); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	detail, err := d.TranscriptDetail(item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail.Transcript, "许宁**") || !strings.Contains(detail.Transcript, "片段2-1**") {
		t.Fatal("append lost manual name or conflated speakers")
	}
	var unknown []UnknownSpeaker
	b, _ := os.ReadFile(filepath.Join(runDir, "unidentified.json"))
	json.Unmarshal(b, &unknown)
	for _, entry := range unknown {
		if entry.Speaker == "片段1-1" && !entry.Labeled {
			t.Fatal("append reset labeled state")
		}
	}
}

func TestReadingLegacyVersionsDoesNotChangeArchive(t *testing.T) {
	d, store, item := testTranscriptionDesktop(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"text":"new"}`)) })
	dir := filepath.Join(store.Root, item.Directory)
	os.WriteFile(filepath.Join(dir, "transcript.md"), []byte("old text"), 0600)
	runs, err := d.TranscriptVersions(item.ID)
	if err != nil || len(runs) != 1 || runs[0].ID != "legacy" {
		t.Fatalf("virtual legacy missing: %v %v", runs, err)
	}
	if _, err = os.Stat(filepath.Join(dir, "transcripts")); !os.IsNotExist(err) {
		t.Fatal("browsing changed the legacy archive")
	}
	detail, err := d.TranscriptDetail(item.ID, "legacy")
	if err != nil || detail.Transcript != "old text" {
		t.Fatal("virtual legacy not readable")
	}
	if err = d.Transcribe(item.ID, "configured"); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	detail, err = d.TranscriptDetail(item.ID, "legacy")
	if err != nil || detail.Transcript != "old text" {
		t.Fatal("legacy alias did not follow preserved original")
	}
}

func TestNewAttemptWaitsForNormalRecordingFinalization(t *testing.T) {
	d, store, item := testTranscriptionDesktop(t, func(w http.ResponseWriter, r *http.Request) { t.Error("ASR called for unfinished recording") })
	item.Active = true
	item.Mode = "meeting"
	store.Save(item)
	if err := d.Transcribe(item.ID, "configured"); err == nil || !strings.Contains(err.Error(), "结束录音") {
		t.Fatalf("unfinished recording accepted: %v", err)
	}
	runs, _ := meeting.ListRuns(filepath.Join(store.Root, item.Directory))
	if len(runs) != 0 {
		t.Fatal("created an unusable attempt")
	}
}
func TestRetryAutomaticFinalizationDoesNotRetranscribeCompletedClips(t *testing.T) {
	calls := 0
	d, store, item := testTranscriptionDesktop(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{"text":"saved clip"}`)) })
	item.Mode = "desk"
	item.Active = true
	store.Save(item)
	s := d.GetSettings()
	dir := filepath.Join(store.Root, item.Directory)
	clip := filepath.Join(dir, "clip.wav")
	os.WriteFile(clip, []byte("clip"), 0600)
	if err := d.enqueueDeskClip(item.ID, 1, clip, 0, 0, s); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	d.dataMu.Lock()
	current, _ := store.Find(item.ID)
	current.Active = false
	store.Save(current)
	d.dataMu.Unlock()
	collision := filepath.Join(store.Root, item.ID+"-saved clip")
	os.Mkdir(collision, 0700)
	if err := d.FinalizeDeskTranscription(item.ID, s); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	versions, _ := d.TranscriptVersions(item.ID)
	if versions[0].State != "failed" || versions[0].Clips[0].State != "complete" {
		t.Fatalf("expected finalization failure only: %+v", versions)
	}
	os.Remove(collision)
	if err := d.RetryTranscription(item.ID); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	versions, _ = d.TranscriptVersions(item.ID)
	if versions[0].State != "complete" || calls != 1 {
		t.Fatalf("finalization retry reran ASR: %+v calls=%d", versions, calls)
	}
}

func TestClosingFailedDeskRunRetainsASRError(t *testing.T) {
	d, store, item := testTranscriptionDesktop(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	item.Mode = "desk"
	item.Active = true
	store.Save(item)
	s := d.GetSettings()
	dir := filepath.Join(store.Root, item.Directory)
	clip := filepath.Join(dir, "clip.wav")
	os.WriteFile(clip, []byte("clip"), 0600)
	if err := d.enqueueDeskClip(item.ID, 1, clip, 0, 0, s); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	versions, _ := d.TranscriptVersions(item.ID)
	originalError := versions[0].Error
	d.dataMu.Lock()
	current, _ := store.Find(item.ID)
	current.Active = false
	store.Save(current)
	d.dataMu.Unlock()
	if err := d.FinalizeDeskTranscription(item.ID, s); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	versions, _ = d.TranscriptVersions(item.ID)
	if versions[0].Error != originalError || !strings.Contains(originalError, "503") {
		t.Fatalf("finalization hid ASR failure: %q -> %q", originalError, versions[0].Error)
	}
}

func TestRecoverOpenAutomaticRunAllowsFinalizationRetry(t *testing.T) {
	store := meeting.Store{Root: t.TempDir()}
	item, _ := store.Create(time.Now())
	dir := filepath.Join(store.Root, item.Directory)
	run, _ := meeting.CreateRun(dir, "test", "configured", json.RawMessage(`{}`), true)
	run.State = "partial"
	run.Clips = []meeting.TranscriptClip{{Index: 1, State: "complete", Transcribed: true, Polished: true}}
	meeting.SaveRun(dir, run)
	if err := RecoverTranscriptJobs(store.Root); err != nil {
		t.Fatal(err)
	}
	run, _ = meeting.LoadRun(dir, run.ID)
	if !run.Closed || run.State != "failed" || run.Clips[0].State != "complete" {
		t.Fatal("open partial run cannot be finished after restart")
	}
}

func TestManualDeskAttemptSnapshotsAudioBeforeQueueRuns(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	received := make(chan string, 1)
	d, store, item := testTranscriptionDesktop(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		b, _ := io.ReadAll(file)
		file.Close()
		received <- string(b)
		w.Write([]byte(`{"text":"snapshot"}`))
	})
	item.Mode = "desk"
	item.Active = true
	store.Save(item)
	q := d.transcriptionQueue()
	q.execute = func(ctx context.Context, job transcriptionJob) error {
		close(entered)
		<-release
		return d.executeTranscription(ctx, job, &transcriptionCredentials{})
	}
	if err := d.Transcribe(item.ID, "configured"); err != nil {
		t.Fatal(err)
	}
	<-entered
	path := filepath.Join(store.Root, item.Directory, item.Audio)
	d.dataMu.Lock()
	out, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		_, err = out.WriteString(" later conversation")
		out.Close()
	}
	d.dataMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	waitTranscriptTests(t, d)
	if got := <-received; got != "original recording" {
		t.Fatalf("queued snapshot changed: %q", got)
	}
	current, _ := store.Find(item.ID)
	if current.Directory != item.Directory || !current.Active {
		t.Fatal("manual ASR disrupted ongoing desk capture")
	}
}

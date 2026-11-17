package main

import (
	"context"
	"encoding/json"
	"fmt"
	"mettings.local/desktop/internal/meeting"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func titleFixture(t *testing.T, endpoint string) (*Desktop, meeting.Store, meeting.Session, meeting.TranscriptRun, transcriptionJob) {
	t.Helper()
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	s := defaultSettings()
	s.Root = t.TempDir()
	s.TitleEndpoint = endpoint
	s.TitleModel = "fixture"
	d := &Desktop{settings: s}
	store := meeting.Store{Root: s.Root}
	item, err := store.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	item.Active = true
	item.Mode = "desk"
	store.Save(item)
	config, _ := json.Marshal(s)
	run, err := meeting.CreateRun(filepath.Join(s.Root, item.Directory), "model", "qwen3", config, true)
	if err != nil {
		t.Fatal(err)
	}
	run.Clips = []meeting.TranscriptClip{{Index: 1, State: "complete"}}
	run.Transcribed = true
	run.Polished = true
	meeting.SaveRun(filepath.Join(s.Root, item.Directory), run)
	dir, _ := meeting.RunDir(filepath.Join(s.Root, item.Directory), run.ID)
	os.WriteFile(filepath.Join(dir, "transcript.md"), []byte("# 转写稿\n\nSynthetic discussion about product plans"), 0600)
	os.WriteFile(filepath.Join(dir, "transcript.json"), []byte(`{"result":{"text":"Synthetic discussion about product plans"}}`), 0600)
	return d, store, item, run, transcriptionJob{s.Root, item.ID, run.ID, 1}
}

func TestDeskTitleGeneratedAtFirstTextAndCompletion(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"讨论标题%d"}}]}`, calls)
	}))
	defer server.Close()
	d, store, item, run, job := titleFixture(t, server.URL)
	if err := d.finishTranscription(context.Background(), job, d.GetSettings(), ""); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Find(item.ID)
	if got.Title != "讨论标题1" || got.Directory != item.Directory || got.Summarized {
		t.Fatalf("bad initial title: %+v", got)
	}
	if err := d.finishTranscription(context.Background(), job, d.GetSettings(), ""); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("title requested for every fragment")
	}
	got.Active = false
	store.Save(got)
	run, _ = meeting.LoadRun(filepath.Join(store.Root, got.Directory), run.ID)
	run.Closed = true
	meeting.SaveRun(filepath.Join(store.Root, got.Directory), run)
	job.ClipIndex = -2
	if err := d.finishTranscription(context.Background(), job, d.GetSettings(), ""); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Find(item.ID)
	if calls != 2 || got.Title != "讨论标题2" || got.Directory == item.Directory || !got.Summarized {
		t.Fatalf("bad final title: %+v calls=%d", got, calls)
	}
	if err := d.finishTranscription(context.Background(), job, d.GetSettings(), ""); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("final title requested twice")
	}
}

func TestManualRegenerateUsesCurrentServiceAndPreservesTitleOnFailure(t *testing.T) {
	fail := false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if fail {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"重新生成的标题"}}]}`)
	}))
	defer server.Close()
	d, store, item, _, job := titleFixture(t, server.URL)
	if err := d.finishTranscription(context.Background(), job, d.GetSettings(), ""); err != nil {
		t.Fatal(err)
	}
	if err := d.RegenerateTitle(item.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Find(item.ID)
	if got.Title != "重新生成的标题" || got.TitleSource != "manual" || got.Directory != item.Directory {
		t.Fatalf("bad active regeneration: %+v", got)
	}
	fail = true
	if err := d.RegenerateTitle(item.ID); err == nil {
		t.Fatal("failure not exposed")
	}
	after, _ := store.Find(item.ID)
	if after.Title != got.Title || after.Directory != got.Directory {
		t.Fatal("failed request changed title")
	}
	if calls != 3 {
		t.Fatal(calls)
	}
}

func TestExplicitRenameSurvivesLaterTitlePublication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":"自动标题"}}]}`)
	}))
	defer server.Close()
	d, store, item, run, job := titleFixture(t, server.URL)
	item.Active = false
	store.Save(item)
	if err := d.RenameSession(item.ID, "我的标题"); err != nil {
		t.Fatal(err)
	}
	item, _ = store.Find(item.ID)
	run.Closed = true
	meeting.SaveRun(filepath.Join(store.Root, item.Directory), run)
	if err := d.finishTranscription(context.Background(), job, d.GetSettings(), ""); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Find(item.ID)
	if got.Title != "我的标题" {
		t.Fatal("manual title overwritten")
	}
}

func TestFinalTitleFailureKeepsProvisionalTitleAndSuccessClearsWarning(t *testing.T) {
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"有效标题"}}]}`)
	}))
	defer server.Close()
	d, store, item, run, job := titleFixture(t, server.URL)
	if err := d.finishTranscription(context.Background(), job, d.GetSettings(), ""); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Find(item.ID)
	got.Active = false
	store.Save(got)
	run, _ = meeting.LoadRun(filepath.Join(store.Root, got.Directory), run.ID)
	run.Closed = true
	meeting.SaveRun(filepath.Join(store.Root, got.Directory), run)
	fail = true
	if err := d.finishTranscription(context.Background(), job, d.GetSettings(), ""); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Find(item.ID)
	if got.Title != "有效标题" || got.Warning != "标题服务不可用，已保留现有标题。" {
		t.Fatalf("bad failure fallback: %+v", got)
	}
	run, _ = meeting.LoadRun(filepath.Join(store.Root, got.Directory), run.ID)
	run.Summarized = false
	meeting.SaveRun(filepath.Join(store.Root, got.Directory), run)
	fail = false
	if err := d.finishTranscription(context.Background(), job, d.GetSettings(), ""); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Find(item.ID)
	if got.Warning != "" {
		t.Fatal("stale failure warning", got.Warning)
	}
}

func TestDeskKeepsTitleServiceWhileForcingLocalASR(t *testing.T) {
	d, r, store := newDeskCoordinatorFixture(t, true)
	r.settings.ASR.Kind = "compatible"
	r.settings.TitleEndpoint = "https://title.example/v1"
	r.settings.TitleModel = "title-model"
	q := d.transcriptionQueue()
	q.execute = func(ctx context.Context, job transcriptionJob) error {
		item, err := store.Find(job.SessionID)
		if err != nil {
			return err
		}
		run, err := meeting.LoadRun(filepath.Join(store.Root, item.Directory), job.RunID)
		if err != nil {
			return err
		}
		var saved Settings
		if err = json.Unmarshal(run.Settings, &saved); err != nil {
			return err
		}
		if saved.ASR.Kind != "local" || saved.TitleEndpoint != r.settings.TitleEndpoint || saved.TitleModel != "title-model" {
			t.Errorf("Desk changed wrong service settings: kind=%s titleConfigured=%t", saved.ASR.Kind, saved.TitleEndpoint != "")
		}
		return nil
	}
	if err := d.handleDeskEvent(r, coordinatorClip(t, r, "voice.wav", 1000, 1)); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
}

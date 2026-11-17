package main

import (
	"mettings.local/desktop/internal/meeting"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestEmptyRerunDoesNotEraseSuccessfulTranscript(t *testing.T) {
	count := 0
	d, store, item := testTranscriptionDesktop(t, func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			w.Write([]byte(`{"text":"A successful transcript."}`))
		} else {
			w.Write([]byte(`{"text":""}`))
		}
	})
	if err := d.Transcribe(item.ID, "configured"); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	first, _ := store.Find(item.ID)
	before, _ := os.ReadFile(filepath.Join(store.Root, first.Directory, "transcript.md"))
	if err := d.Transcribe(item.ID, "configured"); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	runs, _ := d.TranscriptVersions(item.ID)
	if runs[0].State != "empty" || runs[0].Error != "" {
		t.Fatalf("no-text result became failure: %+v", runs[0])
	}
	current, _ := store.Find(item.ID)
	after, _ := os.ReadFile(filepath.Join(store.Root, current.Directory, "transcript.md"))
	if current.ActiveRun != first.ActiveRun || string(before) != string(after) {
		t.Fatal("empty run replaced good transcript")
	}
}
func TestEmptyDeskClipDoesNotFailConversation(t *testing.T) {
	d, store, item := testTranscriptionDesktop(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"text":""}`)) })
	item.Mode = "desk"
	item.Active = true
	store.Save(item)
	source := filepath.Join(store.Root, item.Directory, item.Audio)
	if err := d.enqueueDeskClip(item.ID, 1, source, 0, 0, d.GetSettings()); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	item, _ = store.Find(item.ID)
	item.Active = false
	store.Save(item)
	if err := d.FinalizeDeskTranscription(item.ID, d.GetSettings()); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	runs, _ := d.TranscriptVersions(item.ID)
	if len(runs) != 1 || runs[0].State != "empty" || runs[0].Clips[0].State != "empty" || runs[0].Error != "" {
		t.Fatalf("empty desk run failed: %+v", runs)
	}
	item, _ = store.Find(item.ID)
	if item.Error != "" || item.ActiveRun != "" {
		t.Fatalf("empty transcript was published: %+v", item)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("audio lost", err)
	}
}

func TestDeskRetrySkipsEmptyClips(t *testing.T) {
	var calls atomic.Int32
	d, store, item := testTranscriptionDesktop(t, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.Write([]byte(`{"text":""}`))
		case 2:
			w.WriteHeader(503)
		default:
			w.Write([]byte(`{"text":"Recovered speech."}`))
		}
	})
	item.Mode = "desk"
	item.Active = true
	store.Save(item)
	audio := filepath.Join(store.Root, item.Directory, item.Audio)
	if err := d.enqueueDeskClip(item.ID, 1, audio, 0, 0, d.GetSettings()); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	if err := d.enqueueDeskClip(item.ID, 2, audio, 1000, 5000, d.GetSettings()); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	item, _ = store.Find(item.ID)
	item.Active = false
	store.Save(item)
	d.FinalizeDeskTranscription(item.ID, d.GetSettings())
	waitTranscriptTests(t, d)
	if err := d.Retry(item.ID); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	runs, _ := d.TranscriptVersions(item.ID)
	if calls.Load() != 3 || runs[0].State != "complete" || runs[0].Clips[0].State != "empty" {
		t.Fatalf("empty clip was rerun or prevented completion: calls=%d run=%+v", calls.Load(), runs[0])
	}
}

func TestRetryAfterEmptyCheckpointDoesNotPublishBlankText(t *testing.T) {
	var calls atomic.Int32
	d, store, item := testTranscriptionDesktop(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Write([]byte(`{"text":"Keep this transcript."}`))
		} else {
			w.Write([]byte(`{"text":""}`))
		}
	})
	d.Transcribe(item.ID, "configured")
	waitTranscriptTests(t, d)
	first, _ := store.Find(item.ID)
	d.Transcribe(item.ID, "configured")
	waitTranscriptTests(t, d)
	runs, _ := d.TranscriptVersions(item.ID)
	dir := filepath.Join(store.Root, first.Directory)
	run, err := meeting.LoadRun(dir, runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	run.State = "failed"
	run.Error = "final state write interrupted"
	meeting.SaveRun(dir, run)
	if err = d.Retry(item.ID); err != nil {
		t.Fatal(err)
	}
	waitTranscriptTests(t, d)
	current, _ := store.Find(item.ID)
	runs, _ = d.TranscriptVersions(item.ID)
	if current.ActiveRun != first.ActiveRun || runs[0].State != "empty" || calls.Load() != 2 {
		t.Fatal("retry of empty checkpoint replaced a valid transcript")
	}
}

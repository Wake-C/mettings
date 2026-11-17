package main

import (
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRenameAudioOnlyDoesNotPretendASRCompleted(t *testing.T) {
	s := defaultSettings()
	s.Root = t.TempDir()
	store := meeting.Store{Root: s.Root}
	item, err := store.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(s.Root, item.Directory, item.Audio), []byte("recording"), 0600)
	d := &Desktop{settings: s}
	if err = d.RenameSession(item.ID, "临时讨论"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Find(item.ID)
	if err != nil || got.Title != "临时讨论" || got.Transcribed || got.Summarized {
		t.Fatalf("rename changed transcript stage: %+v %v", got, err)
	}
	b, err := os.ReadFile(filepath.Join(s.Root, got.Directory, got.Audio))
	if err != nil || string(b) != "recording" {
		t.Fatal("audio lost", err)
	}
	got.Active = true
	store.Save(got)
	if err = d.RenameSession(got.ID, "进行中的讨论"); err == nil {
		t.Fatal("renamed active capture")
	}
}

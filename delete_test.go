package main

import (
	"errors"
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteMeetingMovesWholeFolderAndPreservesOtherMeetings(t *testing.T) {
	s := defaultSettings()
	s.Root = t.TempDir()
	store := meeting.Store{Root: s.Root}
	item, _ := store.Create(time.Now())
	other, _ := store.Create(time.Now())
	source := filepath.Join(s.Root, item.Directory)
	os.WriteFile(filepath.Join(source, "recording.wav"), []byte("audio"), 0600)
	os.WriteFile(filepath.Join(source, "transcript.md"), []byte("text"), 0600)
	d := Desktop{settings: s, current: item.ID}
	target := filepath.Join(t.TempDir(), "trashed")
	if err := d.deleteSession(s.Root, item.ID, func(path string) error { return os.Rename(path, target) }); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"status.json", "recording.wav", "transcript.md"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Find(item.ID); err == nil {
		t.Fatal("deleted meeting remains listed")
	}
	if _, err := store.Find(other.ID); err != nil {
		t.Fatal("other meeting affected", err)
	}
	if d.current != "" {
		t.Fatal("current selection not cleared")
	}
}

func TestDeleteMeetingRejectsActiveQueuedAndTrashFailure(t *testing.T) {
	s := defaultSettings()
	s.Root = t.TempDir()
	store := meeting.Store{Root: s.Root}
	item, _ := store.Create(time.Now())
	d := Desktop{settings: s}
	called := false
	trash := func(string) error { called = true; return errors.New("cannot trash") }
	item.Active = true
	store.Save(item)
	if err := d.deleteSession(s.Root, item.ID, trash); err == nil || called {
		t.Fatal("active recording reached trash")
	}
	item.Active = false
	store.Save(item)
	run, err := meeting.CreateRun(filepath.Join(s.Root, item.Directory), "model", "qwen3", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.deleteSession(s.Root, item.ID, trash); err == nil || called {
		t.Fatal("queued recording reached trash")
	}
	run.State = "complete"
	meeting.SaveRun(filepath.Join(s.Root, item.Directory), run)
	if err = d.deleteSession(s.Root, item.ID, trash); err == nil || !called {
		t.Fatal("trash failure not returned")
	}
	if _, err = store.Find(item.ID); err != nil {
		t.Fatal("failure removed recording")
	}
}

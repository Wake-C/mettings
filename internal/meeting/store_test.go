package meeting

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestArchiveAndResume(t *testing.T) {
	root := t.TempDir()
	store := Store{Root: root}
	now := time.Date(2026, 10, 29, 10, 2, 3, 0, time.Local)
	a, err := store.Create(now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Create(now)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("collision")
	}
	a.Transcribed = true
	if err = store.Save(a); err != nil {
		t.Fatal(err)
	}
	a, err = store.Archive(a, "Hello / world")
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Find(a.ID)
	if err != nil || !got.Transcribed || got.Title != "Hello world" {
		t.Fatalf("resume after rename: %+v %v", got, err)
	}
	if _, err = os.Stat(filepath.Join(root, a.Directory, "status.json")); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyArchiveVisible(t *testing.T) {
	root := t.TempDir()
	name := "2026-08-04-171728-讨论记录"
	dir := filepath.Join(root, name)
	os.Mkdir(dir, 0700)
	os.WriteFile(filepath.Join(dir, "status.json"), []byte(`{"transcribed":true,"polished":true,"summarized":true,"started_at":"2026-08-04T17:17:28"}`), 0600)
	os.WriteFile(filepath.Join(dir, "recording.m4a"), []byte("audio"), 0600)
	items, err := (Store{Root: root}).List()
	if err != nil || len(items) != 1 {
		t.Fatalf("legacy archive missing: %v %v", items, err)
	}
	if items[0].ID != "2026-08-04-171728" || items[0].Title != "讨论记录" || items[0].Audio != "recording.m4a" {
		t.Fatalf("wrong legacy metadata %+v", items[0])
	}
}

func TestPendingSpeakersSurviveArchiveRename(t *testing.T) {
	store := Store{Root: t.TempDir()}
	item, err := store.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	WriteJSON(filepath.Join(store.Root, item.Directory, "unidentified.json"), []map[string]any{{"spk": "1"}, {"spk": "2", "labeled": true}})
	item, err = store.Archive(item, "讨论")
	if err != nil {
		t.Fatal(err)
	}
	list, err := store.List()
	if err != nil || len(list) != 1 || list[0].PendingSpeakers != 1 {
		t.Fatalf("pending voices missing: %+v %v", list, err)
	}
}

func TestCreateDoesNotReuseArchivedSessionID(t *testing.T) {
	store := Store{Root: t.TempDir()}
	stamp := time.Date(2026, 10, 29, 10, 0, 0, 0, time.Local)
	first, err := store.Create(stamp)
	if err != nil {
		t.Fatal(err)
	}
	first, err = store.Archive(first, "第一次讨论")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create(stamp)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("archiving made a session ID reusable")
	}
	found, err := store.Find(first.ID)
	if err != nil || found.Directory != first.Directory {
		t.Fatal("new session shadowed the archived one", err)
	}
}

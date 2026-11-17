package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveBrowserListsFoldersWithoutStatus(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "旧会议"), 0700)
	os.WriteFile(filepath.Join(root, "旧会议", "transcript.md"), []byte("# 讨论\n内容"), 0600)
	os.WriteFile(filepath.Join(root, "旧会议", "recording.wav"), []byte("audio"), 0600)
	d := &Desktop{settings: Settings{Root: root}}
	view, err := d.BrowseArchive("")
	if err != nil || len(view.Entries) != 1 || !view.Entries[0].Directory {
		t.Fatalf("missing folder: %+v %v", view, err)
	}
	view, err = d.BrowseArchive("旧会议")
	if err != nil || len(view.Entries) != 2 {
		t.Fatalf("missing files: %+v %v", view, err)
	}
	text, err := d.ReadArchiveFile("旧会议/transcript.md")
	if err != nil || text != "# 讨论\n内容" {
		t.Fatalf("wrong transcript %q %v", text, err)
	}
	if _, err = d.ReadArchiveFile("../outside.md"); err == nil {
		t.Fatal("escaped selected archive")
	}
}

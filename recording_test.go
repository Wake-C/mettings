package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecordingStopKeepsAudioWithoutTranscribing(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	for _, c := range componentsForModel("qwen3") {
		for _, rel := range c.Files {
			path := filepath.Join(runtimeDir(), rel)
			os.MkdirAll(filepath.Dir(path), 0700)
			os.WriteFile(path, []byte("fixture"), 0600)
		}
		os.WriteFile(filepath.Join(runtimeDir(), "."+c.ID+".installed"), []byte(c.SHA), 0600)
	}
	script := "#!/bin/sh\nfor output do :; done\nprintf 'fixture recording' > \"$output\"\nread answer\nexit 0\n"
	os.WriteFile(ffmpegPath(), []byte(script), 0755)
	os.Chmod(ffmpegPath(), 0755)
	s := defaultSettings()
	s.Root = t.TempDir()
	d := &Desktop{settings: s}
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	if err := d.Start(); err == nil {
		t.Fatal("parallel recording accepted")
	}
	if err := d.Stop(false); err != nil {
		t.Fatal(err)
	}
	state, err := d.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if state.Recording || state.Busy || len(state.Sessions) != 1 {
		t.Fatalf("wrong stopped state %+v", state)
	}
	item := state.Sessions[0]
	b, err := os.ReadFile(filepath.Join(s.Root, item.Directory, item.Audio))
	if err != nil || string(b) != "fixture recording" {
		t.Fatal("recording not preserved")
	}
	if item.Transcribed {
		t.Fatal("quit path transcribed audio")
	}
}

func TestFinishUsesCurrentAutoChoiceWithoutASRModels(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	os.MkdirAll(filepath.Dir(ffmpegPath()), 0700)
	os.WriteFile(filepath.Join(runtimeDir(), ".ffmpeg.installed"), []byte(components[0].SHA), 0600)
	script := "#!/bin/sh\nfor output do :; done\nprintf 'saved without ASR' > \"$output\"\nread answer\nexit 0\n"
	os.WriteFile(ffmpegPath(), []byte(script), 0755)
	s := defaultSettings()
	s.Root = t.TempDir()
	s.MeetingAutoASR = true
	d := &Desktop{settings: s}
	if runtimeReady(s.LocalModel) {
		t.Fatal("test accidentally has ASR models")
	}
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	if err := d.SetRecordingOptions("meeting", false); err != nil {
		t.Fatal(err)
	}
	if err := d.Finish(); err != nil {
		t.Fatal(err)
	}
	state, err := d.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Sessions) != 1 || state.Sessions[0].Active || state.JobsRunning || state.Queued != 0 {
		t.Fatalf("save-only state %+v", state)
	}
	item := state.Sessions[0]
	if item.Transcribed {
		t.Fatal("save-only recording transcribed")
	}
	if _, err = os.Stat(filepath.Join(s.Root, item.Directory, "transcripts")); !os.IsNotExist(err) {
		t.Fatal("save-only finish created a transcript run")
	}
	b, err := os.ReadFile(filepath.Join(s.Root, item.Directory, item.Audio))
	if err != nil || string(b) != "saved without ASR" {
		t.Fatal("audio was not kept", err)
	}
	settings, err := loadSettings()
	if err != nil || settings.MeetingAutoASR {
		t.Fatal("auto choice not remembered", err)
	}
}

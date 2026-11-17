package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"mettings.local/desktop/internal/meeting"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The full Desk route uses public fixture speech, real native detection and ASR.
// No microphone, personal conversation, online ASR or API key is used.
func TestNativeDeskConversationsAndAutomaticTranscript(t *testing.T) {
	fixtures := os.Getenv("METTINGS_NATIVE_FIXTURES")
	if fixtures == "" {
		t.Skip("set METTINGS_NATIVE_FIXTURES for native Desk acceptance")
	}
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	server := httptest.NewServer(http.FileServer(http.Dir(fixtures)))
	defer server.Close()
	archives := map[string]string{"ffmpeg": "ffmpeg.zip", "sensevoice": "sensevoice.tar.bz2", "campplus": "campplus.onnx", "segmentation": "segmentation.tar.bz2", "silero-vad": "silero_vad.onnx"}
	plan := append(componentsForModel("sensevoice"), vadComponent)
	for _, c := range plan {
		c.URL = server.URL + "/" + archives[c.ID]
		if err := installComponent(context.Background(), runtimeDir(), c, func(int64, int64) {}); err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
	}
	sample, err := readDeskTestWave(filepath.Join(fixtures, "models", localModelDirectory("sensevoice"), "test_wavs", "zh.wav"))
	if err != nil {
		t.Fatal(err)
	}
	speech := append([]float32{}, sample...)
	speech = append(speech, make([]float32, 96*16000)...)
	speech = append(speech, sample...)
	pcm := make([]byte, len(speech)*4)
	for i, x := range speech {
		binary.LittleEndian.PutUint32(pcm[4*i:], math.Float32bits(x))
	}
	s := defaultSettings()
	s.LocalModel = "sensevoice"
	s.DeskAutoASR = true
	s.Root = t.TempDir()
	d := &Desktop{settings: s}
	r := &deskCapture{started: time.Date(2026, 10, 29, 13, 0, 0, 0, time.Local), settings: s, spool: t.TempDir()}
	d.desk = r
	d.deskState = DeskState{Active: true}
	cmd := exec.Command(deskHelperPath(), filepath.Join(runtimeDir(), "silero_vad.onnx"), r.spool)
	cmd.Stdin = bytes.NewReader(pcm)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("native capture", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(out))
	for decoder.More() {
		var event captureEvent
		if err = decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if err = d.handleDeskEvent(r, event); err != nil {
			t.Fatal(err)
		}
	}
	if err = d.finishDeskConversation(r, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err = d.WaitTranscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	sessions, err := (meeting.Store{Root: s.Root}).List()
	if err != nil || len(sessions) != 2 {
		t.Fatalf("conversation split: %d %v", len(sessions), err)
	}
	for _, session := range sessions {
		if session.Active || session.Mode != "desk" || session.SpeechMS <= 0 || session.SpeechMS > 15000 || session.ClipCount == 0 {
			t.Fatalf("unexpected retained session %+v", session)
		}
		detail, e := d.TranscriptDetail(session.ID, "")
		if e != nil || !strings.Contains(detail.Transcript, "时间") {
			t.Fatalf("missing native transcript %s %v", detail.Transcript, e)
		}
		runs, e := d.TranscriptVersions(session.ID)
		if e != nil || len(runs) != 1 || !runs[0].Automatic || runs[0].State != "complete" {
			t.Fatalf("automatic run: %+v %v", runs, e)
		}
		if _, e = os.Stat(filepath.Join(s.Root, session.Directory, session.Audio)); e != nil {
			t.Fatal("retained audio missing", e)
		}
	}
}

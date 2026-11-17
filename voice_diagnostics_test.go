package main

import (
	"math"
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVoiceMatchExplainsThresholdAndAmbiguity(t *testing.T) {
	library := VoiceLibrary{Voices: map[string][]float64{"person": {1, 0}}}
	vector := []float64{.4805, math.Sqrt(1 - .4805*.4805)}
	name, reason := matchVoiceWithReason(vector, library, .6)
	if name != "" || !strings.Contains(reason, "0.48") || !strings.Contains(reason, "0.60") {
		t.Fatalf("missing threshold explanation: %q", reason)
	}
	if matchVoice(vector, library, .6) != "" {
		t.Fatal("diagnostics changed rejection")
	}
	name, reason = matchVoiceWithReason([]float64{1, 0}, library, .6)
	if name == "" || reason != "" {
		t.Fatal("successful match changed")
	}
	library.Voices["other"] = []float64{1, 0}
	name, reason = matchVoiceWithReason([]float64{1, 0}, library, .6)
	if name != "" || !strings.Contains(reason, "分数相同") {
		t.Fatal("ambiguity unexplained")
	}
	_, reason = matchVoiceWithReason(vector, VoiceLibrary{}, .6)
	if !strings.Contains(reason, "尚未保存") {
		t.Fatal("empty library unexplained")
	}
}

func TestDetailReportsGlobalVoiceCountsAcrossFolders(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(voiceLibrary()), 0700); err != nil {
		t.Fatal(err)
	}
	library := VoiceLibrary{Model: voiceModelID, Voices: map[string][]float64{"one": {1, 0}, "two": {0, 1}}, Samples: map[string][][]float64{"one": {{.8, .2}, {.9, .1}}}}
	if err := meeting.WriteJSON(voiceLibrary(), library); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		s := defaultSettings()
		s.Root = t.TempDir()
		store := meeting.Store{Root: s.Root}
		item, err := store.Create(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		d := Desktop{settings: s}
		detail, err := d.TranscriptDetail(item.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if detail.VoiceProfileCount != 2 || detail.VoiceSampleCount != 4 {
			t.Fatalf("wrong shared counts: %d/%d", detail.VoiceProfileCount, detail.VoiceSampleCount)
		}
	}
}

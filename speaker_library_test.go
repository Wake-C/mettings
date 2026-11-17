package main

import (
	"context"
	"encoding/json"
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLaterConfirmedSampleIsSharedAcrossMeetings(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	os.MkdirAll(filepath.Dir(voiceLibrary()), 0700)
	meeting.WriteJSON(voiceLibrary(), VoiceLibrary{Model: voiceModelID, Voices: map[string][]float64{"Alice": {1, 0}}})
	dir := t.TempDir()
	meeting.WriteJSON(filepath.Join(dir, "unidentified.json"), []UnknownSpeaker{{Speaker: "1", Segment: "other-microphone.wav"}})
	os.WriteFile(filepath.Join(dir, "transcript.md"), []byte("**[00:00:01] 1**：hello"), 0600)
	calls := 0
	embed := func(context.Context, string) ([]float64, error) { calls++; return []float64{0, 1}, nil }
	if err := labelVoiceWithEmbed(context.Background(), defaultSettings(), dir, "1", "Alice", embed); err != nil {
		t.Fatal(err)
	}
	library, err := loadVoiceLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || matchVoice([]float64{0, 1}, library, .6) != "Alice" {
		t.Fatal("confirming an existing person discarded their later voice sample")
	}
	if matchVoice([]float64{1, 0}, library, .6) != "Alice" {
		t.Fatal("new sample replaced the original voice")
	}
	other := t.TempDir()
	meeting.WriteJSON(filepath.Join(other, "unidentified.json"), []UnknownSpeaker{{Speaker: "2", Segment: "new-meeting.wav"}})
	os.WriteFile(filepath.Join(other, "transcript.md"), []byte("**[00:00:01] 2**：hello"), 0600)
	if err = rematchSpeakers(context.Background(), defaultSettings(), other, library, embed); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(other, "transcript.md"))
	if !strings.Contains(string(b), "] Alice**") {
		t.Fatal("later sample not shared across meetings")
	}
}

func TestAttributeReentryPreservesConfirmedIdentity(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "transcript.json"), []byte(`{"result":{"utterances":[{"text":"hello","start_time":0,"end_time":2000,"additions":{"speaker":"1"}}]}}`), 0600)
	os.WriteFile(filepath.Join(dir, "transcript.md"), []byte("**[00:00:00] Alice**：hello"), 0600)
	meeting.WriteJSON(filepath.Join(dir, "speaker_map.json"), map[string]string{"1": "Alice"})
	meeting.WriteJSON(filepath.Join(dir, "unidentified.json"), []UnknownSpeaker{{Speaker: "1", Segment: "manual.wav", Labeled: true}})
	pending, err := attribute(context.Background(), defaultSettings(), dir, "absent.wav")
	if err != nil {
		t.Fatal(err)
	}
	var mapping map[string]string
	b, _ := os.ReadFile(filepath.Join(dir, "speaker_map.json"))
	json.Unmarshal(b, &mapping)
	if pending || mapping["1"] != "Alice" {
		t.Fatalf("confirmed mapping lost on re-entry: pending=%v mapping=%v", pending, mapping)
	}
}

func TestCorrectAutomaticNameUsesSpeakerIDAndCanCorrectAgain(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "transcript.json"), []byte(`{"result":{"utterances":[{"text":"one","start_time":0,"end_time":1000,"additions":{"speaker":"1"}},{"text":"two","start_time":1000,"end_time":2000,"additions":{"speaker":"2"}}]}}`), 0600)
	os.WriteFile(filepath.Join(dir, "transcript.md"), []byte("**[00:00:00] Alice**：one\n**[00:00:01] Alice**：two"), 0600)
	meeting.WriteJSON(filepath.Join(dir, "speaker_map.json"), map[string]string{"1": "Alice", "2": "Alice"})
	meeting.WriteJSON(filepath.Join(dir, "unidentified.json"), []UnknownSpeaker{{Speaker: "1", Name: "Alice", Segment: "one.wav"}, {Speaker: "2", Name: "Alice", Segment: "two.wav"}})
	embed := func(context.Context, string) ([]float64, error) { return []float64{1, 0}, nil }
	for _, name := range []string{"Bob", "Carol"} {
		if err := labelVoiceWithEmbed(context.Background(), defaultSettings(), dir, "1", name, embed); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(dir, "transcript.md"))
		if !strings.Contains(string(b), "] "+name+"**") || !strings.Contains(string(b), "] Alice**") {
			t.Fatalf("correction changed wrong speaker: %s", b)
		}
	}
	library, err := loadVoiceLibrary()
	if err != nil || matchVoice([]float64{1, 0}, library, .6) != "Carol" {
		t.Fatal("corrected manual sample still belongs to previous name")
	}
}

func TestHistoricalOmittedAutomaticSpeakerNeedsRebuild(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "transcript.json"), []byte(`{"result":{"utterances":[{"text":"one","start_time":0,"end_time":1000,"additions":{"speaker":"1"}}]}}`), 0600)
	meeting.WriteJSON(filepath.Join(dir, "unidentified.json"), []UnknownSpeaker{})
	missing, err := missingSpeakerClips(dir)
	if err != nil || !missing {
		t.Fatalf("missing historical speaker undetected: %v %v", missing, err)
	}
	meeting.WriteJSON(filepath.Join(dir, "unidentified.json"), []UnknownSpeaker{{Speaker: "1", Name: "Alice", Timeline: "pcm-v1"}})
	missing, err = missingSpeakerClips(dir)
	if err != nil || missing {
		t.Fatalf("existing index needlessly rebuilt: %v %v", missing, err)
	}
}

func TestSpeakerSamplesDeduplicatePromoteAndRejectIdentityTies(t *testing.T) {
	library := VoiceLibrary{Model: voiceModelID, Voices: map[string][]float64{"Alice": {1, 0}}}
	library.addSample("Alice", []float64{0, 1})
	library.addSample("Alice", []float64{0, 1})
	if len(library.Samples["Alice"]) != 1 {
		t.Fatal("duplicate confirmation grows library")
	}
	library.removeSample("Alice", []float64{1, 0})
	if matchVoice([]float64{0, 1}, library, .6) != "Alice" || len(library.Samples["Alice"]) != 0 {
		t.Fatal("remaining sample not promoted")
	}
	library.addSample("Bob", []float64{0, 1})
	for range 20 {
		if matchVoice([]float64{0, 1}, library, .6) != "" {
			t.Fatal("equal identities arbitrarily matched")
		}
	}
}

func TestDetailExposesHistoricalSpeakersWithoutChangingFiles(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	s := defaultSettings()
	s.Root = t.TempDir()
	store := meeting.Store{Root: s.Root}
	item, err := store.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Root, item.Directory)
	os.WriteFile(filepath.Join(dir, "transcript.json"), []byte(`{"result":{"utterances":[{"text":"one","start_time":0,"end_time":1000,"additions":{"speaker":"1"}}]}}`), 0600)
	meeting.WriteJSON(filepath.Join(dir, "unidentified.json"), []UnknownSpeaker{})
	d := Desktop{settings: s}
	detail, err := d.TranscriptDetail(item.ID, "")
	if err != nil || !detail.HasSpeakers || len(detail.Unidentified) != 0 {
		t.Fatalf("historical match control unavailable: %+v %v", detail, err)
	}
	var entries []UnknownSpeaker
	b, _ := os.ReadFile(filepath.Join(dir, "unidentified.json"))
	json.Unmarshal(b, &entries)
	if len(entries) != 0 {
		t.Fatal("read rewrote historical speaker index")
	}
}

package main

import "testing"

func TestVoiceProfileUsesConfirmedSampleCentroid(t *testing.T) {
	library := VoiceLibrary{Voices: map[string][]float64{"person": {1, 0, 0, 0}}, Samples: map[string][][]float64{"person": {{0, 1, 0, 0}, {0, 0, 1, 0}, {0, 0, 0, 1}}}}
	if name := matchVoice([]float64{.5, .5, .5, .5}, library, .6); name != "person" {
		t.Fatal("confirmed samples were scored only in isolation")
	}
	if name := matchVoice([]float64{0, 0, 0, 0}, library, .6); name != "" {
		t.Fatal("invalid vector matched")
	}
}

func TestSessionEvidenceHelpsShortSpeechWithoutChainingOrEnrollment(t *testing.T) {
	library := VoiceLibrary{Voices: map[string][]float64{"person": {1, 0}}}
	vectors := map[string][]float64{
		"anchor": {.8, .6}, "short": {.5, .8660254}, "chain-only": {.1, .9949874}, "different": {-.8, .6},
	}
	matches := matchSpeakerVectors(vectors, library, .6)
	if matches["anchor"].Name != "person" || matches["short"].Name != "person" {
		t.Fatal("consistent session evidence was ignored")
	}
	if matches["chain-only"].Name != "" {
		t.Fatal("inferred match became another anchor")
	}
	if matches["different"].Name != "" {
		t.Fatal("different speaker acquired identity")
	}
	if len(library.Voices) != 1 || len(library.Samples) != 0 {
		t.Fatal("machine match enrolled itself")
	}
}

func TestSessionEvidenceRejectsCompetingAnchors(t *testing.T) {
	library := VoiceLibrary{Voices: map[string][]float64{"A": {1, 0, 0}, "B": {0, 1, 0}}}
	matches := matchSpeakerVectors(map[string][]float64{"anchorA": {.8, 0, .6}, "anchorB": {0, .8, .6}, "uncertain": {.55, .5, .6689544}}, library, .6)
	if matches["anchorA"].Name != "A" || matches["anchorB"].Name != "B" {
		t.Fatal("bad test anchors")
	}
	if matches["uncertain"].Name != "" {
		t.Fatal("conflicting anchors assigned an identity")
	}
}

func TestSessionEvidencePreservesManualIdentityAndMissingCache(t *testing.T) {
	library := VoiceLibrary{Voices: map[string][]float64{"person": {1, 0}}}
	entries := []UnknownSpeaker{{Speaker: "manual", Name: "manual name", Labeled: true}, {Speaker: "old", Name: "saved name"}, {Speaker: "new"}}
	names := map[string]string{"manual": "manual name", "old": "saved name"}
	applySpeakerMatches(entries, names, map[string][]float64{"manual": {1, 0}, "new": {1, 0}}, library, .6)
	if names["manual"] != "manual name" || names["old"] != "saved name" || names["new"] != "person" {
		t.Fatal("manual identity or uncached historical data changed")
	}
}

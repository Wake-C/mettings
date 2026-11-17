package main

import (
	"math"
	"testing"
)

func TestPooledUnknownGroupRecognizesWithoutDirectAnchor(t *testing.T) {
	library := VoiceLibrary{Voices: map[string][]float64{"one": {1, 0, 0}}}
	z := math.Sqrt(1 - .58*.58 - .7*.7)
	vectors := map[string][]float64{"a": {.58, .7, z}, "b": {.58, .7, -z}}
	for _, v := range vectors {
		if matchVoice(v, library, .6) != "" {
			t.Fatal("fixture has direct identity anchor")
		}
	}
	entries := []UnknownSpeaker{{Speaker: "a"}, {Speaker: "b"}}
	names := map[string]string{}
	applySpeakerMatches(entries, names, vectors, library, .6)
	if names["a"] != "one" || names["b"] != "one" {
		t.Fatalf("pooled consistent group not identified: %v", names)
	}
	if entries[0].Labeled || entries[1].Labeled || len(library.Samples) != 0 {
		t.Fatal("automatic match became manual enrollment")
	}
}

func TestPooledGroupPreservesSeparateAndConfirmedVoices(t *testing.T) {
	library := VoiceLibrary{Voices: map[string][]float64{"one": {1, 0, 0}}}
	z := math.Sqrt(1 - .58*.58 - .7*.7)
	vectors := map[string][]float64{"a": {.58, .7, z}, "b": {.58, .7, -z}, "other": {0, -1, 0}}
	entries := []UnknownSpeaker{{Speaker: "a", Name: "manual-name", Labeled: true}, {Speaker: "b"}, {Speaker: "other"}}
	names := map[string]string{"a": "manual-name"}
	applySpeakerMatches(entries, names, vectors, library, .6)
	if names["a"] != "manual-name" || names["b"] != "" || names["other"] != "" {
		t.Fatalf("borrowed evidence from a confirmed different voice: %v", names)
	}
}

func TestPooledGroupBelowThresholdStaysUnknown(t *testing.T) {
	library := VoiceLibrary{Voices: map[string][]float64{"one": {1, 0, 0}}}
	vectors := map[string][]float64{"a": {.2, .9, .3}, "b": {.2, .9, -.3}}
	entries := []UnknownSpeaker{{Speaker: "a"}, {Speaker: "b"}}
	names := map[string]string{}
	applySpeakerMatches(entries, names, vectors, library, .6)
	if len(names) != 0 {
		t.Fatalf("clustering was mistaken for known identity: %v", names)
	}
}

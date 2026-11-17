package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mettings.local/desktop/internal/meeting"
)

func angleVoice(degrees float64) []float64 {
	a := degrees * math.Pi / 180
	return []float64{math.Cos(a), math.Sin(a)}
}
func TestSpeakerGroupsDeduplicateNamesAndUnknownVoicesWithoutChains(t *testing.T) {
	entries := []UnknownSpeaker{{Speaker: "a", Start: 0, End: 1000}, {Speaker: "b", Start: 1000, End: 5000}, {Speaker: "c", Start: 5000, End: 6000}, {Speaker: "d", Start: 6000, End: 7000, Name: "Alice", Labeled: true}, {Speaker: "e", Start: 7000, End: 9000, Name: "Alice"}, {Speaker: "f", Start: 9000, End: 10000, Name: "Bob", Labeled: true}, {Speaker: "g", Start: 10000, End: 11000}}
	vectors := map[string][]float64{"a": angleVoice(0), "b": angleVoice(35), "c": angleVoice(70), "d": angleVoice(0), "e": angleVoice(0), "f": angleVoice(0)}
	groups := groupSpeakers(entries, vectors, .6)
	if len(groups) != 5 {
		t.Fatalf("got %d groups: %+v", len(groups), groups)
	}
	for _, g := range groups {
		if g.Name == "" && g.Count > 2 {
			t.Fatal("transitive voice chain merged")
		}
		if g.Name == "Alice" && (g.Count != 2 || g.Confirmed || g.Representative != "e") {
			t.Fatalf("named grouping wrong: %+v", g)
		}
		if g.Name == "Bob" && (!g.Confirmed || g.Count != 1) {
			t.Fatal("manual identity conflict merged")
		}
		if g.ID == "g" && g.Count != 1 {
			t.Fatal("missing embedding merged")
		}
	}
	reversed := append([]UnknownSpeaker(nil), entries...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	if !reflect.DeepEqual(groups, groupSpeakers(reversed, vectors, .6)) {
		t.Fatal("grouping depends on input order")
	}
}

func TestSpeakerGroupIDAndRepresentativeAcrossAppend(t *testing.T) {
	entries := []UnknownSpeaker{{Speaker: "片段2-1", Start: 100, End: 2000}, {Speaker: "片段3-1", Start: 3000, End: 7000}}
	vectors := map[string][]float64{"片段2-1": {1, 0}, "片段3-1": {1, 0}, "片段10-1": {1, 0}}
	first := groupSpeakers(entries, vectors, .6)[0]
	entries = append(entries, UnknownSpeaker{Speaker: "片段10-1", Start: 10000, End: 11000})
	next := groupSpeakers(entries, vectors, .6)[0]
	if first.ID != next.ID || next.ID != "片段2-1" || next.Representative != "片段3-1" {
		t.Fatalf("unstable group: %+v %+v", first, next)
	}
}

func groupLabelFixture(t *testing.T) (Settings, string) {
	t.Helper()
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	s := defaultSettings()
	dir := t.TempDir()
	entries := []UnknownSpeaker{{Speaker: "a", Segment: "a.wav", Start: 0, End: 2000}, {Speaker: "b", Segment: "b.wav", Start: 2000, End: 6000}, {Speaker: "new", Segment: "new.wav", Start: 7000, End: 8000}, {Speaker: "other", Segment: "other.wav", Start: 9000, End: 10000}}
	vectors := map[string][]float64{"a": {1, 0}, "b": {1, 0}, "new": {1, 0}, "other": {0, 1}}
	var result meeting.Result
	for _, e := range entries {
		result.Result.Utterances = append(result.Result.Utterances, meeting.Utterance{Start: e.Start, End: e.End, Text: "test", Additions: meeting.Speaker{Speaker: e.Speaker}})
	}
	for name, v := range map[string]any{"unidentified.json": entries, "speaker_embeddings.json": vectors, "speaker_map.json": map[string]string{}, "transcript.json": result} {
		if err := meeting.WriteJSON(filepath.Join(dir, name), v); err != nil {
			t.Fatal(err)
		}
	}
	return s, dir
}

func TestGroupConfirmationOnlyChangesReviewedMembersAndEnrollsRepresentative(t *testing.T) {
	s, dir := groupLabelFixture(t)
	calls := []string{}
	embed := func(_ context.Context, path string) ([]float64, error) {
		calls = append(calls, filepath.Base(path))
		return []float64{1, 0}, nil
	}
	if err := labelSpeakerGroupWithEmbed(context.Background(), s, dir, []string{"a", "b"}, "Alice", embed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"b.wav"}) {
		t.Fatalf("enrolled every segment: %v", calls)
	}
	library, err := loadVoiceLibrary()
	if err != nil || len(library.Voices) != 1 || len(library.Samples["Alice"]) != 0 {
		t.Fatal("wrong global enrollment", err)
	}
	entries, _, _, err := readSpeakerGroups(dir, .6)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Speaker == "a" || entry.Speaker == "b" {
			if entry.Name != "Alice" || !entry.Labeled {
				t.Fatal("group member unlabeled")
			}
		} else if entry.Name != "" || entry.Labeled {
			t.Fatal("unreviewed appended voice changed")
		}
	}
	before, _ := os.ReadFile(voiceLibrary())
	if err = labelSpeakerGroupWithEmbed(context.Background(), s, dir, []string{"new", "other"}, "Wrong", embed); err == nil {
		t.Fatal("accepted different voice groups")
	}
	after, _ := os.ReadFile(voiceLibrary())
	if string(before) != string(after) {
		t.Fatal("rejected group mutated library")
	}
}

func TestGroupLabelRejectsDuplicatesAndKeepsManualIdentitySamplesCorrect(t *testing.T) {
	s, dir := groupLabelFixture(t)
	embed := func(_ context.Context, _ string) ([]float64, error) { return []float64{1, 0}, nil }
	for _, ids := range [][]string{nil, {"a", "a"}, {"missing"}} {
		if err := labelSpeakerGroupWithEmbed(context.Background(), s, dir, ids, "Alice", embed); err == nil {
			t.Fatalf("accepted %v", ids)
		}
	}
	if err := labelSpeakerGroupWithEmbed(context.Background(), s, dir, []string{"a", "b"}, "Alice", embed); err != nil {
		t.Fatal(err)
	}
	if err := labelSpeakerGroupWithEmbed(context.Background(), s, dir, []string{"a", "b"}, "Bob", embed); err != nil {
		t.Fatal(err)
	}
	lib, err := loadVoiceLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lib.Voices["Alice"]; ok {
		t.Fatal("renamed confirmation kept old sample")
	}
	if matchVoice([]float64{1, 0}, lib, .6) != "Bob" {
		t.Fatal("new identity missing")
	}
	var entries []UnknownSpeaker
	b, _ := os.ReadFile(filepath.Join(dir, "unidentified.json"))
	json.Unmarshal(b, &entries)
	if entries[0].Name != "Bob" || entries[1].Name != "Bob" {
		t.Fatal("group rename lost member")
	}
}

func TestGroupRenamePreservesUnreviewedManualSamples(t *testing.T) {
	s, dir := groupLabelFixture(t)
	entries, vectors, _, err := readSpeakerGroups(dir, .6)
	if err != nil {
		t.Fatal(err)
	}
	samples := map[string][]float64{"a": {1, 0}, "b": {.8, .6}, "new": {.6, .8}}
	names := map[string]string{}
	for i := range entries {
		if samples[entries[i].Speaker] != nil {
			entries[i].Name = "Alice"
			entries[i].Labeled = true
			names[entries[i].Speaker] = "Alice"
			vectors[entries[i].Speaker] = samples[entries[i].Speaker]
		}
	}
	library := VoiceLibrary{Model: voiceModelID, Voices: map[string][]float64{"Alice": samples["a"]}, Samples: map[string][][]float64{"Alice": {samples["b"], samples["new"]}}}
	if err = os.MkdirAll(filepath.Dir(voiceLibrary()), 0700); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]any{voiceLibrary(): library, filepath.Join(dir, "unidentified.json"): entries, filepath.Join(dir, "speaker_map.json"): names, filepath.Join(dir, "speaker_embeddings.json"): vectors} {
		if err = meeting.WriteJSON(path, value); err != nil {
			t.Fatal(err)
		}
	}
	calls := []string{}
	embed := func(_ context.Context, path string) ([]float64, error) {
		id := strings.TrimSuffix(filepath.Base(path), ".wav")
		calls = append(calls, id)
		return samples[id], nil
	}
	if err = labelSpeakerGroupWithEmbed(context.Background(), s, dir, []string{"a", "b"}, "Bob", embed); err != nil {
		t.Fatal(err)
	}
	got, err := loadVoiceLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Voices["Alice"], samples["new"]) || len(got.Samples["Alice"]) != 0 {
		t.Fatal("unreviewed manual profile sample removed")
	}
	if !reflect.DeepEqual(got.Voices["Bob"], samples["b"]) || len(got.Samples["Bob"]) != 0 {
		t.Fatal("wrong representative enrollment")
	}
	if !reflect.DeepEqual(calls, []string{"a", "b"}) {
		t.Fatalf("unreviewed sample re-enrolled: %v", calls)
	}
	entries, _, _, err = readSpeakerGroups(dir, .6)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Speaker == "new" && (entry.Name != "Alice" || !entry.Labeled) {
			t.Fatal("unreviewed manual name changed")
		}
	}
}

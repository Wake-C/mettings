package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mettings.local/desktop/internal/meeting"
)

// SpeakerGroup is a review unit, not a new global identity. Original speaker
// IDs remain available for listening and correcting an individual segment.
type SpeakerGroup struct {
	ID             string   `json:"id"`
	Name           string   `json:"name,omitempty"`
	Confirmed      bool     `json:"confirmed"`
	MemberIDs      []string `json:"memberIDs"`
	Representative string   `json:"representative"`
	Count          int      `json:"count"`
	Reason         string   `json:"reason,omitempty"`
}

// Complete-link clustering requires every pair in an unknown group to pass
// the similarity threshold. An A-B and B-C chain cannot silently merge A-C.
// Named voices are grouped only by their existing canonical name.
func groupSpeakers(entries []UnknownSpeaker, vectors map[string][]float64, threshold float64) []SpeakerGroup {
	if threshold <= 0 {
		threshold = .6
	}
	sorted := append([]UnknownSpeaker(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		return sorted[i].Speaker < sorted[j].Speaker
	})
	byID := map[string]UnknownSpeaker{}
	known := map[string][]string{}
	clusters := [][]string{}
	for _, entry := range sorted {
		if entry.Speaker == "" {
			continue
		}
		if _, exists := byID[entry.Speaker]; exists {
			continue
		}
		byID[entry.Speaker] = entry
		if entry.Name != "" {
			known[entry.Name] = append(known[entry.Name], entry.Speaker)
		} else {
			clusters = append(clusters, []string{entry.Speaker})
		}
	}
	// Cache pair similarities; native embedding inference never runs during UI reads.
	similarity := map[[2]string]float64{}
	for i, a := range clusters {
		for _, b := range clusters[i+1:] {
			av, bv := vectors[a[0]], vectors[b[0]]
			score := math.Inf(-1)
			if len(av) > 0 && len(av) == len(bv) {
				score = meeting.Similarity(av, bv)
				if math.IsNaN(score) {
					score = math.Inf(-1)
				}
			}
			similarity[[2]string{a[0], b[0]}] = score
			similarity[[2]string{b[0], a[0]}] = score
		}
	}
	for {
		ai, bi := -1, -1
		best := math.Inf(-1)
		for i := 0; i < len(clusters); i++ {
			for j := i + 1; j < len(clusters); j++ {
				minimum := 1.0
				for _, a := range clusters[i] {
					for _, b := range clusters[j] {
						if value := similarity[[2]string{a, b}]; value < minimum {
							minimum = value
						}
					}
				}
				if minimum >= threshold && minimum > best {
					best, ai, bi = minimum, i, j
				}
			}
		}
		if ai < 0 {
			break
		}
		clusters[ai] = append(clusters[ai], clusters[bi]...)
		clusters = append(clusters[:bi], clusters[bi+1:]...)
	}
	groups := []SpeakerGroup{}
	makeGroup := func(ids []string, name string) SpeakerGroup {
		// Chronological first member stays stable when later clips append.
		first, rep := ids[0], ids[0]
		confirmed := name != ""
		for _, id := range ids {
			entry := byID[id]
			if !entry.Labeled {
				confirmed = false
			}
			if entry.Start < byID[first].Start || entry.Start == byID[first].Start && id < first {
				first = id
			}
			if entry.End-entry.Start > byID[rep].End-byID[rep].Start || entry.End-entry.Start == byID[rep].End-byID[rep].Start && id < rep {
				rep = id
			}
		}
		members := append([]string(nil), ids...)
		sort.Strings(members)
		reason := ""
		if name == "" {
			reason = byID[rep].Reason
			if len(ids) > 1 {
				reason = "这些片段的声音相近，请试听后确认姓名"
			}
		}
		return SpeakerGroup{ID: first, Name: name, Confirmed: confirmed, MemberIDs: members, Representative: rep, Count: len(ids), Reason: reason}
	}
	for _, ids := range clusters {
		groups = append(groups, makeGroup(ids, ""))
	}
	for name, ids := range known {
		groups = append(groups, makeGroup(ids, name))
	}
	sort.Slice(groups, func(i, j int) bool {
		a, b := byID[groups[i].ID], byID[groups[j].ID]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		return groups[i].ID < groups[j].ID
	})
	return groups
}

func readSpeakerGroups(dir string, threshold float64) ([]UnknownSpeaker, map[string][]float64, []SpeakerGroup, error) {
	var entries []UnknownSpeaker
	data, err := os.ReadFile(filepath.Join(dir, "unidentified.json"))
	if err != nil {
		return nil, nil, nil, err
	}
	if err = json.Unmarshal(data, &entries); err != nil {
		return nil, nil, nil, err
	}
	var names map[string]string
	if data, e := os.ReadFile(filepath.Join(dir, "speaker_map.json")); e == nil {
		if e = json.Unmarshal(data, &names); e != nil {
			return nil, nil, nil, e
		}
	}
	for i := range entries {
		if name := names[entries[i].Speaker]; name != "" {
			entries[i].Name = name
		}
	}
	vectors := map[string][]float64{}
	// An old recording without cached vectors remains reviewable as singletons.
	if data, e := os.ReadFile(filepath.Join(dir, "speaker_embeddings.json")); e == nil {
		_ = json.Unmarshal(data, &vectors)
	}
	if vectors == nil {
		vectors = map[string][]float64{}
	}
	return entries, vectors, groupSpeakers(entries, vectors, threshold), nil
}

func (d *Desktop) LabelSpeakerGroup(id, runID string, memberIDs []string, name string) error {
	return d.labelSpeakerGroup(id, runID, memberIDs, name, embedVoice)
}
func (d *Desktop) labelSpeakerGroup(id, runID string, memberIDs []string, name string, embed func(context.Context, string) ([]float64, error)) error {
	if !d.labelMu.TryLock() {
		return errors.New("转写处理中，请稍后确认声音")
	}
	defer d.labelMu.Unlock()
	s := d.GetSettings()
	item, dir, err := transcriptPath(s.Root, id, runID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err = labelSpeakerGroupWithEmbed(ctx, s, dir, memberIDs, name, embed); err != nil {
		return err
	}
	if runID == "" {
		runID = item.ActiveRun
	}
	if runID != "" && runID != "legacy" {
		return d.PublishTranscriptVersion(id, runID)
	}
	d.dataMu.Lock()
	defer d.dataMu.Unlock()
	store := meeting.Store{Root: s.Root}
	if current, e := store.Find(id); e == nil && current.PendingSpeakers == 0 && current.Warning == "部分说话人尚未识别。可播放片段并标注姓名。" {
		current.Warning = ""
		return store.Save(current)
	}
	return nil
}

func labelSpeakerGroupWithEmbed(ctx context.Context, s Settings, dir string, memberIDs []string, name string, embed func(context.Context, string) ([]float64, error)) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "\n\r*[]\\") {
		return errors.New("请输入有效姓名")
	}
	var aliases map[string]string
	_ = json.Unmarshal([]byte(s.Aliases), &aliases)
	if canonical := aliases[name]; canonical != "" {
		name = canonical
	}
	entries, vectors, groups, err := readSpeakerGroups(dir, s.VoiceThreshold)
	if err != nil {
		return err
	}
	selected := map[string]bool{}
	for _, id := range memberIDs {
		if id == "" || selected[id] {
			return errors.New("声音分组已变化，请刷新后重试")
		}
		selected[id] = true
	}
	if len(selected) == 0 {
		return errors.New("请选择要确认的声音")
	}
	// Accept a displayed group after new clips append, but never apply the name
	// to new members that the user did not review or to a now-divided group.
	valid := false
	for _, group := range groups {
		count := 0
		for _, id := range group.MemberIDs {
			if selected[id] {
				count++
			}
		}
		if count == len(selected) {
			valid = true
			break
		}
	}
	if !valid {
		return errors.New("声音分组已变化，请刷新后分别确认")
	}
	indices := []int{}
	rep := -1
	for i, entry := range entries {
		if selected[entry.Speaker] {
			indices = append(indices, i)
			if rep < 0 || entry.End-entry.Start > entries[rep].End-entries[rep].Start || entry.End-entry.Start == entries[rep].End-entries[rep].Start && entry.Speaker < entries[rep].Speaker {
				rep = i
			}
		}
	}
	library, err := loadVoiceLibrary()
	if err != nil {
		return err
	}
	var representative []float64
	for _, i := range indices {
		entry := entries[i]
		// Renaming a prior manual confirmation removes its exact old sample. Only
		// the representative is added; other grouped segments are not auto-trained.
		if i != rep && (!entry.Labeled || entry.Name == "" || entry.Name == name) {
			continue
		}
		clip := filepath.Join(dir, entry.Segment)
		if filepath.Ext(clip) != ".wav" {
			target := filepath.Join(dir, "group-label-input.wav")
			if err = wav(ctx, clip, target, 0, 0); err != nil {
				return err
			}
			defer os.Remove(target)
			clip = target
		}
		vector, e := embed(ctx, clip)
		if e != nil {
			return e
		}
		vectors[entry.Speaker] = vector
		if entry.Labeled && entry.Name != "" && entry.Name != name {
			library.removeSample(entry.Name, vector)
		}
		if i == rep {
			representative = vector
		}
	}
	library.addSample(name, representative)
	if err = os.MkdirAll(filepath.Dir(voiceLibrary()), 0700); err != nil {
		return err
	}
	if err = meeting.WriteJSON(voiceLibrary(), library); err != nil {
		return err
	}
	names := map[string]string{}
	for i := range entries {
		if selected[entries[i].Speaker] {
			entries[i].Name = name
			entries[i].Labeled = true
			entries[i].Reason = ""
		}
		if entries[i].Name != "" {
			names[entries[i].Speaker] = entries[i].Name
		}
	}
	if err = writeSpeakerTranscript(dir, s, names); err != nil {
		return err
	}
	if err = meeting.WriteJSON(filepath.Join(dir, "speaker_map.json"), names); err != nil {
		return err
	}
	if err = meeting.WriteJSON(filepath.Join(dir, "speaker_embeddings.json"), vectors); err != nil {
		return err
	}
	return meeting.WriteJSON(filepath.Join(dir, "unidentified.json"), entries)
}

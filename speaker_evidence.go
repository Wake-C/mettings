package main

import (
	"math"
	"mettings.local/desktop/internal/meeting"
)

type speakerMatch struct{ Name, Reason string }

// normalizedVoiceMean gives each confirmed sample equal weight regardless of
// its vector magnitude. Individual references remain available for voice modes
// which differ from the profile's central representation.
func normalizedVoiceMean(vectors ...[]float64) []float64 {
	var mean []float64
	count := 0
	for _, vector := range vectors {
		norm := 0.0
		for _, v := range vector {
			norm += v * v
		}
		if norm <= 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
			continue
		}
		if mean == nil {
			mean = make([]float64, len(vector))
		}
		if len(vector) != len(mean) {
			continue
		}
		norm = math.Sqrt(norm)
		for i, v := range vector {
			mean[i] += v / norm
		}
		count++
	}
	if count == 0 {
		return nil
	}
	return mean
}

func bestVoiceProfile(vector []float64, library VoiceLibrary) (best string, score float64, ambiguous bool) {
	for name, candidate := range library.Voices {
		personScore := meeting.Similarity(vector, candidate)
		references := [][]float64{candidate}
		for _, sample := range library.Samples[name] {
			references = append(references, sample)
			if value := meeting.Similarity(vector, sample); value > personScore {
				personScore = value
			}
		}
		if len(references) > 1 {
			if value := meeting.Similarity(vector, normalizedVoiceMean(references...)); value > personScore {
				personScore = value
			}
		}
		if personScore > score {
			best, score, ambiguous = name, personScore, false
		} else if personScore == score && best != "" {
			ambiguous = true
		}
	}
	return
}

// matchSpeakerVectors uses only direct global-profile matches as local anchors.
// A short fragment must agree with the anchor's profile and pass both the
// existing speaker-similarity threshold and pooled-profile threshold. Inferred
// matches never become anchors and are never added to the global library.
func matchSpeakerVectors(vectors map[string][]float64, library VoiceLibrary, threshold float64) map[string]speakerMatch {
	if threshold <= 0 {
		threshold = .6
	}
	matches := map[string]speakerMatch{}
	anchors := map[string]string{}
	for id, vector := range vectors {
		name, reason := matchVoiceWithReason(vector, library, threshold)
		matches[id] = speakerMatch{name, reason}
		if name != "" {
			anchors[id] = name
		}
	}
	for id, vector := range vectors {
		if matches[id].Name != "" {
			continue
		}
		best, _, ambiguous := bestVoiceProfile(vector, library)
		if best == "" || ambiguous {
			continue
		}
		candidate := ""
		conflict := false
		for anchorID, name := range anchors {
			if meeting.Similarity(vector, vectors[anchorID]) < threshold {
				continue
			}
			// A competing directly recognized identity rules out propagation.
			if name != best {
				conflict = true
				continue
			}
			pooled, score, tied := bestVoiceProfile(normalizedVoiceMean(vector, vectors[anchorID]), library)
			if pooled != name || score < threshold || tied {
				continue
			}
			if candidate != "" && candidate != name {
				conflict = true
			}
			candidate = name
		}
		if candidate != "" && !conflict {
			matches[id] = speakerMatch{Name: candidate}
		}
	}
	return matches
}

func applySpeakerMatches(entries []UnknownSpeaker, names map[string]string, vectors map[string][]float64, library VoiceLibrary, threshold float64) {
	matches := matchSpeakerVectors(vectors, library, threshold)
	for i := range entries {
		entry := &entries[i]
		if entry.Labeled {
			continue
		}
		match, ok := matches[entry.Speaker]
		if !ok {
			continue
		}
		entry.Name, entry.Reason = match.Name, match.Reason
		if match.Name == "" {
			delete(names, entry.Speaker)
		} else {
			names[entry.Speaker] = match.Name
		}
	}
	applyGroupedSpeakerMatches(entries, names, vectors, library, threshold)
}

// Repeated speech can supply enough evidence even when no individual segment
// passes identification. Grouping is deliberately separate from identification:
// only complete-link groups whose pooled and individual best identity agree
// may receive a name. This never changes confirmed or already named entries.
func applyGroupedSpeakerMatches(entries []UnknownSpeaker, names map[string]string, vectors map[string][]float64, library VoiceLibrary, threshold float64) {
	if len(library.Voices) == 0 || len(vectors) < 2 {
		return
	}
	if threshold <= 0 {
		threshold = .6
	}
	for _, group := range groupSpeakers(entries, vectors, threshold) {
		if group.Name != "" || len(group.MemberIDs) < 2 {
			continue
		}
		pool := make([][]float64, 0, len(group.MemberIDs))
		for _, id := range group.MemberIDs {
			pool = append(pool, vectors[id])
		}
		name, score, tied := bestVoiceProfile(normalizedVoiceMean(pool...), library)
		if name == "" || score < threshold || tied {
			continue
		}
		agree := true
		for _, vector := range pool {
			best, _, ambiguous := bestVoiceProfile(vector, library)
			if best != name || ambiguous {
				agree = false
				break
			}
		}
		if !agree {
			continue
		}
		members := make(map[string]bool, len(group.MemberIDs))
		for _, id := range group.MemberIDs {
			members[id] = true
		}
		for i := range entries {
			entry := &entries[i]
			if !members[entry.Speaker] || entry.Labeled || entry.Name != "" {
				continue
			}
			entry.Name, entry.Reason = name, ""
			names[entry.Speaker] = name
		}
	}
}

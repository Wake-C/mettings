package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
	"time"
)

// ReidentifySpeakers uses the app-wide library on saved speaker clips. It does
// not rerun ASR or change a name that the user has already confirmed.
func (d *Desktop) ReidentifySpeakers(id, runID string) error {
	if !d.labelMu.TryLock() {
		return errors.New("转写处理中，请稍后匹配说话人")
	}
	defer d.labelMu.Unlock()
	s := d.GetSettings()
	item, dir, err := transcriptPath(s.Root, id, runID)
	if err != nil {
		return err
	}
	library, err := loadVoiceLibrary()
	if err != nil {
		return err
	}
	if len(library.Voices) == 0 {
		return errors.New("请先试听片段并保存一位说话人的姓名")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// Older versions omitted automatically named speakers from the clip index.
	// Rebuild that index only on this explicit action, using retained audio.
	if missing, e := missingSpeakerClips(dir); e != nil {
		return e
	} else if missing {
		if _, err = attribute(ctx, s, dir, filepath.Join(s.Root, item.Directory, item.Audio)); err != nil {
			return err
		}
	}
	if err = rematchSpeakers(ctx, s, dir, library, embedVoice); err != nil {
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

func missingSpeakerClips(dir string) (bool, error) {
	b, err := os.ReadFile(filepath.Join(dir, "transcript.json"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var result meeting.Result
	if err = json.Unmarshal(b, &result); err != nil {
		return false, err
	}
	var entries []UnknownSpeaker
	if b, err = os.ReadFile(filepath.Join(dir, "unidentified.json")); err == nil {
		if err = json.Unmarshal(b, &entries); err != nil {
			return false, err
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	known := map[string]bool{}
	for _, entry := range entries {
		if !entry.Labeled && entry.Timeline != "pcm-v1" {
			return true, nil
		}
		known[entry.Speaker] = true
	}
	for _, u := range result.Result.Utterances {
		if u.Additions.Speaker != "" && !known[u.Additions.Speaker] {
			return true, nil
		}
	}
	return false, nil
}

func matchVoice(vector []float64, library VoiceLibrary, threshold float64) string {
	name, _ := matchVoiceWithReason(vector, library, threshold)
	return name
}

func matchVoiceWithReason(vector []float64, library VoiceLibrary, threshold float64) (string, string) {
	if threshold <= 0 {
		threshold = .6
	}
	if len(library.Voices) == 0 {
		return "", "尚未保存声音样本，请试听后确认姓名"
	}
	best, score, ambiguous := bestVoiceProfile(vector, library)
	if score >= threshold && !ambiguous {
		return best, ""
	}
	if score >= threshold && ambiguous {
		return "", "多个声音档案的匹配分数相同，请试听后确认姓名"
	}
	return "", fmt.Sprintf("匹配分数 %.2f，低于阈值 %.2f。请试听后确认姓名", score, threshold)
}

func rematchSpeakers(ctx context.Context, s Settings, dir string, library VoiceLibrary, embed func(context.Context, string) ([]float64, error)) error {
	b, err := os.ReadFile(filepath.Join(dir, "unidentified.json"))
	if err != nil {
		return err
	}
	var entries []UnknownSpeaker
	if err = json.Unmarshal(b, &entries); err != nil {
		return err
	}
	names := map[string]string{}
	if data, e := os.ReadFile(filepath.Join(dir, "speaker_map.json")); e == nil {
		if err = json.Unmarshal(data, &names); err != nil {
			return err
		}
	}
	if names == nil {
		names = map[string]string{}
	}
	vectors := map[string][]float64{}
	for i := range entries {
		entry := &entries[i]
		if entry.Labeled {
			entry.Name = names[entry.Speaker]
			continue
		}
		vector, e := embed(ctx, filepath.Join(dir, entry.Segment))
		if e != nil {
			return e
		}
		vectors[entry.Speaker] = vector
		if name, reason := matchVoiceWithReason(vector, library, s.VoiceThreshold); name != "" {
			names[entry.Speaker] = name
			entry.Name = name
			entry.Reason = ""
		} else {
			delete(names, entry.Speaker)
			entry.Name = ""
			entry.Reason = reason
		}
	}
	applySpeakerMatches(entries, names, vectors, library, s.VoiceThreshold)
	if err = meeting.WriteJSON(filepath.Join(dir, "speaker_embeddings.json"), vectors); err != nil {
		return err
	}
	if err = writeSpeakerTranscript(dir, s, names); err != nil {
		return err
	}
	if err = meeting.WriteJSON(filepath.Join(dir, "speaker_map.json"), names); err != nil {
		return err
	}
	return meeting.WriteJSON(filepath.Join(dir, "unidentified.json"), entries)
}

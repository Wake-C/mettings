package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func (d *Desktop) updateRun(job transcriptionJob, change func(*meeting.TranscriptRun)) (meeting.TranscriptRun, string, error) {
	d.dataMu.Lock()
	defer d.dataMu.Unlock()
	item, err := (meeting.Store{Root: job.Root}).Find(job.SessionID)
	if err != nil {
		return meeting.TranscriptRun{}, "", err
	}
	sessionDir := filepath.Join(job.Root, item.Directory)
	run, err := meeting.LoadRun(sessionDir, job.RunID)
	if err != nil {
		return run, "", err
	}
	change(&run)
	err = meeting.SaveRun(sessionDir, run)
	dir, _ := meeting.RunDir(sessionDir, run.ID)
	return run, dir, err
}
func (d *Desktop) failTranscription(job transcriptionJob, cause error) {
	if cause == nil {
		return
	}
	message := cause.Error()
	if errors.Is(cause, context.Canceled) {
		message = "转写已取消，可稍后重试"
	}
	_, _, _ = d.updateRun(job, func(run *meeting.TranscriptRun) {
		run.State = "failed"
		run.Error = message
		if job.ClipIndex >= 0 {
			for i := range run.Clips {
				if run.Clips[i].Index == job.ClipIndex {
					run.Clips[i].State = "failed"
					run.Clips[i].Error = message
				}
			}
		}
	})
	d.dataMu.Lock()
	defer d.dataMu.Unlock()
	store := meeting.Store{Root: job.Root}
	if item, e := store.Find(job.SessionID); e == nil {
		item.Error = message
		_ = store.Save(item)
	}
}
func (d *Desktop) finishTranscription(ctx context.Context, job transcriptionJob, s Settings, titleKey string) error {
	run, dir, err := d.updateRun(job, func(run *meeting.TranscriptRun) {
		if !run.Automatic {
			run.State = "complete"
			return
		}
		complete := len(run.Clips) > 0
		hasText := false
		failed := ""
		for _, clip := range run.Clips {
			if clip.State == "complete" {
				hasText = true
			}
			if clip.State != "complete" && clip.State != "empty" {
				complete = false
			}
			if clip.State == "failed" {
				failed = clip.Error
			}
		}
		if failed != "" {
			run.State = "failed"
			run.Error = failed
		} else if complete && run.Closed {
			run.Error = ""
			if hasText {
				run.State = "complete"
			} else {
				run.State = "empty"
			}
		} else {
			run.State = "partial"
		}
		run.Transcribed = complete
		run.Polished = complete
	})
	if err != nil {
		return err
	}
	if run.Automatic {
		hasResult := false
		for _, clip := range run.Clips {
			if clip.State == "complete" {
				hasResult = true
				break
			}
		}
		if !hasResult {
			if run.State == "empty" {
				return d.clearEmptyAttemptError(job)
			}
			if run.Error != "" {
				return errors.New(run.Error)
			}
			return nil
		}
	}
	finalTitle := run.State == "complete" && !run.Summarized
	initialTitle := run.Automatic && !run.Closed && !run.InitialTitleDone
	if finalTitle || initialTitle {
		b, e := os.ReadFile(filepath.Join(dir, "transcript.md"))
		if e != nil {
			return e
		}
		title := run.Title
		if title == "" {
			title = fallbackTitle(string(b))
		}
		warning := ""
		d.dataMu.Lock()
		item, lookupErr := (meeting.Store{Root: job.Root}).Find(job.SessionID)
		d.dataMu.Unlock()
		if lookupErr != nil {
			return lookupErr
		}
		manualTitle := item.TitleSource == "manual"
		if manualTitle {
			title = item.Title
		}
		// Title preferences are current service settings, independent of the
		// frozen ASR model used by this run.
		current := d.GetSettings()
		if current.Root != "" {
			if current.TitleEndpoint != s.TitleEndpoint {
				titleKey = readKey(current.TitleEndpoint, "title")
			}
			s.TitleEndpoint, s.TitleModel = current.TitleEndpoint, current.TitleModel
		}
		if !manualTitle && s.TitleEndpoint != "" && s.TitleModel != "" {
			if result, e := generateTitle(ctx, s, string(b), titleKey); e == nil {
				title = result
			} else {
				warning = "标题服务不可用，已使用转写稿首句。"
				if run.Title != "" {
					warning = "标题服务不可用，已保留现有标题。"
				}
			}
		}
		run, dir, err = d.updateRun(job, func(latest *meeting.TranscriptRun) {
			latest.Title = title
			latest.InitialTitleDone = true
			latest.Summarized = finalTitle
			if warning != "" {
				latest.Warning = warning
			} else if latest.Warning == "标题服务不可用，已使用转写稿首句。" || latest.Warning == "标题服务不可用，已保留现有标题。" {
				latest.Warning = ""
			}
		})
		if err != nil {
			return err
		}
	}
	d.dataMu.Lock()
	defer d.dataMu.Unlock()
	if err = d.publishRunLocked(job.Root, job.SessionID, run); err != nil {
		return err
	}
	if job.ClipIndex == -2 && run.State == "failed" && run.Error != "" {
		return errors.New(run.Error)
	}
	return nil
}
func (d *Desktop) publishRunLocked(root, id string, run meeting.TranscriptRun) error {
	store := meeting.Store{Root: root}
	item, err := store.Find(id)
	if err != nil {
		return err
	}
	sessionDir := filepath.Join(root, item.Directory)
	dir, err := meeting.RunDir(sessionDir, run.ID)
	if err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(dir, "transcript.md")); err != nil {
		return err
	}
	if item.ActiveRun != "" && item.ActiveRun != run.ID {
		if active, e := meeting.LoadRun(sessionDir, item.ActiveRun); e == nil && active.CreatedAt > run.CreatedAt {
			return nil
		}
	}
	for _, name := range []string{"transcript.md", "transcript.json", "speaker_map.json", "provider-response.json"} {
		if name == "provider-response.json" {
			if _, e := os.Stat(filepath.Join(dir, name)); os.IsNotExist(e) {
				if e = os.Remove(filepath.Join(sessionDir, name)); e != nil && !os.IsNotExist(e) {
					return e
				}
				continue
			}
		}
		if err = copyArtifact(filepath.Join(dir, name), filepath.Join(sessionDir, name)); err != nil {
			return err
		}
	}
	// Export paths remain valid in the compatibility files at the session root.
	var unknown []UnknownSpeaker
	if b, e := os.ReadFile(filepath.Join(dir, "unidentified.json")); e == nil && json.Unmarshal(b, &unknown) == nil {
		for i := range unknown {
			unknown[i].Segment = filepath.Join("transcripts", run.ID, unknown[i].Segment)
		}
		if err = meeting.WriteJSON(filepath.Join(sessionDir, "unidentified.json"), unknown); err != nil {
			return err
		}
	}
	item.ActiveRun = run.ID
	item.Transcribed = true
	item.Polished = run.Polished
	item.Error = run.Error
	if item.Error == "" {
		item.Error = item.CaptureError
	}
	item.Warning = run.Warning
	if run.Summarized {
		item.Summarized = true
	}
	// Explicit user titles take priority. Active capture keeps a stable folder;
	// only the displayed title changes until capture has closed.
	if run.Title != "" && (item.Title == "" || item.TitleSource == "auto" && item.TitleRunID == run.ID) {
		item.Title = run.Title
		item.TitleSource = "auto"
		item.TitleRunID = run.ID
		if !item.Active {
			_, err = store.Archive(item, run.Title)
			return err
		}
	}
	return store.Save(item)
}
func (d *Desktop) PublishTranscriptVersion(id, runID string) error {
	s := d.GetSettings()
	d.dataMu.Lock()
	defer d.dataMu.Unlock()
	item, err := (meeting.Store{Root: s.Root}).Find(id)
	if err != nil {
		return err
	}
	if runID == "" {
		runID = item.ActiveRun
	}
	if runID == "" {
		return nil
	}
	run, err := meeting.LoadRun(filepath.Join(s.Root, item.Directory), runID)
	if err != nil {
		return err
	}
	if run.Warning == "部分说话人尚未识别。可播放片段并标注姓名。" {
		dir, _ := meeting.RunDir(filepath.Join(s.Root, item.Directory), runID)
		var unknown []UnknownSpeaker
		if b, e := os.ReadFile(filepath.Join(dir, "unidentified.json")); e == nil && json.Unmarshal(b, &unknown) == nil {
			pending := false
			for _, entry := range unknown {
				if !entry.Labeled && entry.Name == "" {
					pending = true
				}
			}
			if !pending {
				run.Warning = ""
				if e = meeting.SaveRun(filepath.Join(s.Root, item.Directory), run); e != nil {
					return e
				}
			}
		}
	}
	return d.publishRunLocked(s.Root, id, run)
}

var transcriptTimestamp = regexp.MustCompile(`\*\*\[(\d+):(\d{2}):(\d{2})\]`)

func shiftedMarkdown(md string, offsetMS int64) string {
	return transcriptTimestamp.ReplaceAllStringFunc(md, func(marker string) string {
		var h, m, s int64
		if _, err := fmt.Sscanf(marker, "**[%d:%d:%d]", &h, &m, &s); err != nil {
			return marker
		}
		total := h*3600 + m*60 + s + offsetMS/1000
		return fmt.Sprintf("**[%02d:%02d:%02d]", total/3600, total%3600/60, total%60)
	})
}
func (d *Desktop) aggregateAutomatic(job transcriptionJob) error {
	d.dataMu.Lock()
	defer d.dataMu.Unlock()
	item, err := (meeting.Store{Root: job.Root}).Find(job.SessionID)
	if err != nil {
		return err
	}
	sessionDir := filepath.Join(job.Root, item.Directory)
	run, err := meeting.LoadRun(sessionDir, job.RunID)
	if err != nil {
		return err
	}
	dir, _ := meeting.RunDir(sessionDir, run.ID)
	timeline, err := readDeskTimeline(sessionDir)
	if err != nil {
		return err
	}
	var settings Settings
	if err = json.Unmarshal(run.Settings, &settings); err != nil {
		return err
	}
	var aliases map[string]string
	_ = json.Unmarshal([]byte(settings.Aliases), &aliases)
	priorNames := map[string]string{}
	if b, e := os.ReadFile(filepath.Join(dir, "speaker_map.json")); e == nil {
		_ = json.Unmarshal(b, &priorNames)
	}
	previous := []UnknownSpeaker{}
	if b, e := os.ReadFile(filepath.Join(dir, "unidentified.json")); e == nil {
		_ = json.Unmarshal(b, &previous)
	}
	labeled := map[string]bool{}
	for _, entry := range previous {
		labeled[entry.Speaker] = entry.Labeled
	}
	clips := append([]meeting.TranscriptClip(nil), run.Clips...)
	sort.Slice(clips, func(i, j int) bool { return clips[i].Index < clips[j].Index })
	var merged meeting.Result
	var body strings.Builder
	body.WriteString("# 转写稿\n\n")
	unknown := []UnknownSpeaker{}
	vectors := map[string][]float64{}
	names := map[string]string{}
	for _, clip := range clips {
		if clip.State != "complete" {
			continue
		}
		prefix := filepath.Join("clips", fmt.Sprintf("%06d", clip.Index))
		work := filepath.Join(dir, prefix)
		var result meeting.Result
		b, e := os.ReadFile(filepath.Join(work, "transcript.json"))
		if e != nil {
			return e
		}
		if e = json.Unmarshal(b, &result); e != nil {
			return e
		}
		stampDeskResult(&result, timeline, clip.Index)
		// Render stable speaker IDs before applying the latest names. A clip's
		// display markdown may contain an older automatic match.
		md := meeting.Correct(meeting.Markdown(result), settings.Corrections)
		md = strings.TrimPrefix(md, "# 转写稿\n")
		body.WriteString(shiftedMarkdown(strings.TrimSpace(md), clip.RetainedOffsetMS))
		body.WriteString("\n\n")
		for _, u := range result.Result.Utterances {
			u.Start += clip.RetainedOffsetMS
			u.End += clip.RetainedOffsetMS
			merged.Result.Utterances = append(merged.Result.Utterances, u)
		}
		if result.Result.Text != "" {
			if merged.Result.Text != "" {
				merged.Result.Text += "\n"
			}
			merged.Result.Text += result.Result.Text
		}
		var entries []UnknownSpeaker
		if b, e = os.ReadFile(filepath.Join(work, "unidentified.json")); e == nil {
			_ = json.Unmarshal(b, &entries)
		}
		for _, entry := range entries {
			entry.Segment = filepath.Join(prefix, entry.Segment)
			entry.Start += clip.RetainedOffsetMS
			entry.End += clip.RetainedOffsetMS
			entry.Labeled = entry.Labeled || labeled[entry.Speaker]
			unknown = append(unknown, entry)
		}
		var mapping map[string]string
		if b, e = os.ReadFile(filepath.Join(work, "speaker_map.json")); e == nil {
			_ = json.Unmarshal(b, &mapping)
		}
		for spk, name := range mapping {
			names[spk] = name
		}
		var clipVectors map[string][]float64
		if b, e = os.ReadFile(filepath.Join(work, "speaker_embeddings.json")); e == nil {
			if e = json.Unmarshal(b, &clipVectors); e != nil {
				return e
			}
			for spk, vector := range clipVectors {
				vectors[spk] = vector
			}
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	for spk, name := range priorNames {
		names[spk] = name
	}
	for i := range unknown {
		unknown[i].Name = names[unknown[i].Speaker]
	}
	if library, e := loadVoiceLibrary(); e == nil {
		applySpeakerMatches(unknown, names, vectors, library, settings.VoiceThreshold)
	}
	if err = meeting.WriteJSON(filepath.Join(dir, "speaker_embeddings.json"), vectors); err != nil {
		return err
	}
	if len(merged.Result.Utterances) == 0 && strings.TrimSpace(merged.Result.Text) == "" {
		body.Reset()
	}
	if err = meeting.WriteJSON(filepath.Join(dir, "transcript.json"), merged); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "transcript.md"), []byte(meeting.MapSpeakers(meeting.MapSpeakers(body.String(), names), aliases)), 0600); err != nil {
		return err
	}
	if err = meeting.WriteJSON(filepath.Join(dir, "speaker_map.json"), names); err != nil {
		return err
	}
	return meeting.WriteJSON(filepath.Join(dir, "unidentified.json"), unknown)
}

// Empty recognition is a completed observation, not a model/service failure.
// Keep the input and version, but never publish it over an existing transcript.
func (d *Desktop) completeWithoutText(ctx context.Context, job transcriptionJob, s Settings, titleKey, work string, result meeting.Result) error {
	if err := meeting.WriteJSON(filepath.Join(work, "transcript.json"), result); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "transcript.md"), nil, 0600); err != nil {
		return err
	}
	if len(result.Raw) > 0 {
		if err := os.WriteFile(filepath.Join(work, "provider-response.json"), result.Raw, 0600); err != nil {
			return err
		}
	}
	run, _, err := d.updateRun(job, func(run *meeting.TranscriptRun) {
		run.Error = ""
		if job.ClipIndex < 0 {
			run.State = "empty"
			run.Transcribed = true
			run.Polished = true
			run.Warning = ""
			return
		}
		for i := range run.Clips {
			if run.Clips[i].Index == job.ClipIndex {
				run.Clips[i].State = "empty"
				run.Clips[i].Error = ""
				run.Clips[i].Transcribed = true
				run.Clips[i].Polished = true
			}
		}
	})
	if err != nil {
		return err
	}
	if run.Automatic {
		if err = d.aggregateAutomatic(job); err != nil {
			return err
		}
		return d.finishTranscription(ctx, job, s, titleKey)
	}
	return d.clearEmptyAttemptError(job)
}
func (d *Desktop) clearEmptyAttemptError(job transcriptionJob) error {
	d.dataMu.Lock()
	defer d.dataMu.Unlock()
	store := meeting.Store{Root: job.Root}
	item, err := store.Find(job.SessionID)
	if err != nil {
		return err
	}
	runs, err := meeting.ListRuns(filepath.Join(job.Root, item.Directory))
	if err != nil {
		return err
	}
	if len(runs) > 0 && runs[0].ID == job.RunID {
		item.Error = item.CaptureError
		return store.Save(item)
	}
	return nil
}

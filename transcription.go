package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type TranscriptionQueueState struct {
	Queued    int    `json:"queued"`
	Running   bool   `json:"running"`
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId"`
}
type transcriptionJob struct {
	Root, SessionID, RunID string
	ClipIndex              int
}
type transcriptionQueue struct {
	execute func(context.Context, transcriptionJob) error // test seam; nil uses the native/provider pipeline
	mu      sync.Mutex
	jobs    []transcriptionJob
	current *transcriptionJob
	running bool
	cancel  context.CancelFunc
	idle    chan struct{}
}

func (d *Desktop) transcriptionQueue() *transcriptionQueue {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.transcripts == nil {
		done := make(chan struct{})
		close(done)
		d.transcripts = &transcriptionQueue{idle: done}
	}
	return d.transcripts
}
func (d *Desktop) TranscriptionStatus() TranscriptionQueueState {
	q := d.transcriptionQueue()
	q.mu.Lock()
	defer q.mu.Unlock()
	state := TranscriptionQueueState{Queued: len(q.jobs), Running: q.current != nil}
	if q.current != nil {
		state.SessionID = q.current.SessionID
		state.RunID = q.current.RunID
	}
	return state
}
func (d *Desktop) WaitTranscriptions(ctx context.Context) error {
	q := d.transcriptionQueue()
	q.mu.Lock()
	done := q.idle
	q.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (d *Desktop) addTranscriptionJob(job transcriptionJob) {
	q := d.transcriptionQueue()
	q.mu.Lock()
	same := func(other transcriptionJob) bool {
		return other.Root == job.Root && other.SessionID == job.SessionID && other.RunID == job.RunID && other.ClipIndex == job.ClipIndex
	}
	if q.current != nil && same(*q.current) {
		q.mu.Unlock()
		return
	}
	for _, other := range q.jobs {
		if same(other) {
			q.mu.Unlock()
			return
		}
	}
	q.jobs = append(q.jobs, job)
	start := !q.running
	if start {
		q.running = true
		q.idle = make(chan struct{})
	}
	q.mu.Unlock()
	if start {
		go d.workTranscriptions(q)
	}
}
func (d *Desktop) workTranscriptions(q *transcriptionQueue) {
	for {
		q.mu.Lock()
		if len(q.jobs) == 0 {
			q.running = false
			q.current = nil
			close(q.idle)
			q.mu.Unlock()
			return
		}
		job := q.jobs[0]
		q.jobs = q.jobs[1:]
		q.current = &job
		ctx, cancel := context.WithCancel(context.Background())
		q.cancel = cancel
		q.mu.Unlock()
		d.labelMu.Lock()
		var result error
		if q.execute != nil {
			result = q.execute(ctx, job)
		} else if job.ClipIndex == -2 {
			result = d.executeFinalization(ctx, job)
		} else {
			result = d.executeTranscription(ctx, job, nil)
		}
		d.labelMu.Unlock()
		if job.ClipIndex < 0 {
			empty := false
			d.dataMu.Lock()
			if item, e := (meeting.Store{Root: job.Root}).Find(job.SessionID); e == nil {
				if run, e := meeting.LoadRun(filepath.Join(job.Root, item.Directory), job.RunID); e == nil {
					empty = run.State == "empty"
				}
			}
			d.dataMu.Unlock()
			if !empty {
				d.notifyTranscription(job.SessionID, result)
			}
		}
		cancel()
		q.mu.Lock()
		q.current = nil
		q.cancel = nil
		q.mu.Unlock()
	}
}
func (d *Desktop) CancelTranscription() {
	q := d.transcriptionQueue()
	q.mu.Lock()
	jobs := append([]transcriptionJob(nil), q.jobs...)
	q.jobs = nil
	if q.cancel != nil {
		q.cancel()
	}
	q.mu.Unlock()
	for _, job := range jobs {
		d.failTranscription(job, errors.New("转写已取消，可稍后重试"))
	}
}
func modelLabel(s Settings) string {
	if s.ASR.Kind == "local" {
		if model, ok := findLocalModel(s.LocalModel); ok {
			return model.Label
		}
		return s.LocalModel
	}
	if s.ASR.Kind == "volc" {
		return "Volcengine SeedASR"
	}
	if s.ASR.Kind == "firered2-llm" {
		return "FireRedASR2-LLM · 8B+ 服务"
	}
	return s.ASR.Model
}
func settingsForChoice(s Settings, choice string) (Settings, error) {
	if _, ok := findLocalModel(choice); ok {
		s.ASR.Kind = "local"
		s.LocalModel = choice
		return s, nil
	}
	if choice == "configured" && s.ASR.Kind != "local" {
		return s, nil
	}
	return s, errors.New("请选择已配置的转写模型")
}
func (d *Desktop) Transcribe(id, choice string) error {
	s, err := settingsForChoice(d.GetSettings(), choice)
	if err != nil {
		return err
	}
	return d.enqueueTranscription(id, s, true)
}
func (d *Desktop) RetryTranscription(id string) error {
	return d.enqueueTranscription(id, d.GetSettings(), false)
}
func (d *Desktop) enqueueTranscription(id string, s Settings, newAttempt bool) error {
	d.dataMu.Lock()
	store := meeting.Store{Root: s.Root}
	item, err := store.Find(id)
	if err != nil {
		d.dataMu.Unlock()
		return err
	}
	if newAttempt && item.Active && item.Mode != "desk" {
		d.dataMu.Unlock()
		return errors.New("请先结束录音，再为这段音频生成转写")
	}
	dir := filepath.Join(s.Root, item.Directory)
	if err = d.preserveLegacyLocked(store, &item, s); err != nil {
		d.dataMu.Unlock()
		return err
	}
	var run meeting.TranscriptRun
	if !newAttempt {
		runs, e := meeting.ListRuns(dir)
		if e != nil {
			d.dataMu.Unlock()
			return e
		}
		for _, candidate := range runs {
			if candidate.State == "failed" {
				run = candidate
				break
			}
		}
		if run.ID == "" {
			d.dataMu.Unlock()
			return errors.New("没有失败的转写任务，请选择模型生成新版本")
		}
		if run.Choice == "legacy" {
			d.dataMu.Unlock()
			return errors.New("原有转写缺少模型信息，请选择模型生成新版本")
		}
		run.State = "queued"
		run.Error = ""
		if err = meeting.SaveRun(dir, run); err != nil {
			d.dataMu.Unlock()
			return err
		}
	} else {
		run, err = d.createRunLocked(item, s, false)
		if err != nil {
			d.dataMu.Unlock()
			return err
		}
	}
	jobs := []transcriptionJob{}
	if run.Automatic {
		for i := range run.Clips {
			if run.Clips[i].State != "complete" && run.Clips[i].State != "empty" {
				run.Clips[i].State = "queued"
				run.Clips[i].Error = ""
				jobs = append(jobs, transcriptionJob{s.Root, id, run.ID, run.Clips[i].Index})
			}
		}
		if len(jobs) == 0 && run.Closed {
			jobs = append(jobs, transcriptionJob{s.Root, id, run.ID, -2})
		}
		err = meeting.SaveRun(dir, run)
	} else {
		jobs = append(jobs, transcriptionJob{s.Root, id, run.ID, -1})
	}
	d.dataMu.Unlock()
	if err != nil {
		return err
	}
	for _, job := range jobs {
		d.addTranscriptionJob(job)
	}
	return nil
}
func (d *Desktop) createRunLocked(item meeting.Session, s Settings, automatic bool) (meeting.TranscriptRun, error) {
	dir := filepath.Join(s.Root, item.Directory)
	settings, _ := json.Marshal(s)
	choice := "configured"
	if s.ASR.Kind == "local" {
		choice = s.LocalModel
	}
	run, err := meeting.CreateRun(dir, modelLabel(s), choice, settings, automatic)
	if err != nil {
		return run, err
	}
	if !automatic {
		source := filepath.Join(dir, item.Audio)
		ext := filepath.Ext(item.Audio)
		if ext == "" {
			ext = ".audio"
		}
		run.Input = "input" + ext
		runDir, _ := meeting.RunDir(dir, run.ID)
		if err = snapshotAudio(source, filepath.Join(runDir, run.Input), !item.Active); err != nil {
			run.State = "failed"
			run.Error = "原始录音不可用：" + err.Error()
			_ = meeting.SaveRun(dir, run)
			return run, errors.New(run.Error)
		}
	}
	return run, meeting.SaveRun(dir, run)
}
func snapshotAudio(source, target string, immutable bool) error {
	if immutable {
		if err := os.Link(source, target); err == nil {
			return nil
		}
	}
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, src)
	closeErr := out.Close()
	if err != nil {
		os.Remove(target)
		return err
	}
	return closeErr
}
func (d *Desktop) preserveLegacyLocked(store meeting.Store, item *meeting.Session, s Settings) error {
	dir := filepath.Join(store.Root, item.Directory)
	if item.ActiveRun != "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, "transcript.md")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	runs, err := meeting.ListRuns(dir)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.Choice == "legacy" {
			item.ActiveRun = run.ID
			return store.Save(*item)
		}
	}
	config, _ := json.Marshal(s)
	run, err := meeting.CreateRun(dir, "原有转写稿", "legacy", config, false)
	if err != nil {
		return err
	}
	run.State = "complete"
	run.Transcribed = true
	run.Polished = item.Polished
	run.Summarized = item.Summarized
	run.Title = item.Title
	out, _ := meeting.RunDir(dir, run.ID)
	for _, name := range []string{"transcript.md", "transcript.json", "speaker_map.json", "provider-response.json", "unidentified.json", "segments"} {
		if err = copyArtifact(filepath.Join(dir, name), filepath.Join(out, name)); err != nil {
			return err
		}
	}
	// Existing unknown-speaker paths may already point into another version.
	if err = meeting.SaveRun(dir, run); err != nil {
		return err
	}
	item.ActiveRun = run.ID
	return store.Save(*item)
}
func copyArtifact(source, target string) error {
	info, err := os.Stat(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err = os.MkdirAll(target, 0700); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err = copyArtifact(filepath.Join(source, e.Name()), filepath.Join(target, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(target+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	closeErr := dst.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(target+".tmp", target)
}
func transcriptPath(root, id, runID string) (meeting.Session, string, error) {
	item, err := (meeting.Store{Root: root}).Find(id)
	if err != nil {
		return item, "", err
	}
	dir := filepath.Join(root, item.Directory)
	if runID == "legacy" {
		runs, e := meeting.ListRuns(dir)
		if e != nil {
			return item, "", e
		}
		for _, run := range runs {
			if run.Choice == "legacy" {
				path, e := meeting.RunDir(dir, run.ID)
				return item, path, e
			}
		}
		return item, dir, nil
	}
	if runID == "" {
		runID = item.ActiveRun
	}
	if runID == "" {
		return item, dir, nil
	}
	runDir, err := meeting.RunDir(dir, runID)
	if err != nil {
		return item, "", err
	}
	if _, err = meeting.LoadRun(dir, runID); err != nil {
		return item, "", err
	}
	return item, runDir, nil
}
func (d *Desktop) TranscriptVersions(id string) ([]meeting.TranscriptRun, error) {
	s := d.GetSettings()
	d.dataMu.Lock()
	defer d.dataMu.Unlock()
	item, err := (meeting.Store{Root: s.Root}).Find(id)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.Root, item.Directory)
	runs, err := meeting.ListRuns(dir)
	if err != nil {
		return nil, err
	}
	legacy := false
	for i := range runs {
		if runs[i].Choice == "legacy" {
			legacy = true
		}
		runs[i].Settings = nil
	}
	if !legacy && item.ActiveRun == "" {
		if _, e := os.Stat(filepath.Join(dir, "transcript.md")); e == nil {
			runs = append(runs, meeting.TranscriptRun{ID: "legacy", Model: "原有转写稿", Choice: "legacy", State: "complete", CreatedAt: item.StartedAt, Transcribed: item.Transcribed, Polished: item.Polished, Summarized: item.Summarized, Title: item.Title, Closed: true})
		}
	}
	return runs, nil
}
func (d *Desktop) TranscriptDetail(id, runID string) (Detail, error) {
	s := d.GetSettings()
	d.dataMu.Lock()
	defer d.dataMu.Unlock()
	item, dir, err := transcriptPath(s.Root, id, runID)
	if err != nil {
		return Detail{}, err
	}
	if runID == "" {
		runID = item.ActiveRun
	}
	result := Detail{Session: item, RunID: runID, Unidentified: []map[string]any{}, Names: []string{}}
	if runID != "" && runID != "legacy" {
		if run, e := meeting.LoadRun(filepath.Join(s.Root, item.Directory), runID); e == nil {
			result.Session.Transcribed = run.Transcribed
			result.Session.Polished = run.Polished
			result.Session.Summarized = run.Summarized
			result.Session.Error = run.Error
			if result.Session.Error == "" {
				result.Session.Error = item.CaptureError
			}
			result.Session.Warning = run.Warning
		}
	}

	b, _ := os.ReadFile(filepath.Join(dir, "transcript.md"))
	result.Transcript = string(b)
	if item.Mode == "desk" {
		if timeline, estimated, e := readDeskDisplayTimeline(filepath.Join(s.Root, item.Directory)); e == nil {
			result.TimestampEstimated = estimated
			var raw meeting.Result
			if data, e := os.ReadFile(filepath.Join(dir, "transcript.json")); e == nil && json.Unmarshal(data, &raw) == nil {
				result.Transcript = datedDeskMarkdown(result.Transcript, timeline, raw.Result.Utterances)
				result.Transcript = strings.ReplaceAll(result.Transcript, "> "+meeting.EstimatedTimestampNotice+"\n\n", "")
			}
		}
	}
	if b, err = os.ReadFile(filepath.Join(dir, "unidentified.json")); err == nil {
		_ = json.Unmarshal(b, &result.Unidentified)
	}
	var mapping map[string]string
	if b, e := os.ReadFile(filepath.Join(dir, "speaker_map.json")); e == nil && json.Unmarshal(b, &mapping) == nil {
		result.HasSpeakers = len(mapping) > 0
		for _, entry := range result.Unidentified {
			if spk, ok := entry["spk"].(string); ok && mapping[spk] != "" {
				entry["name"] = mapping[spk]
			}
		}
	}
	if len(result.Unidentified) > 0 {
		result.HasSpeakers = true
	}
	if !result.HasSpeakers {
		var transcript meeting.Result
		if b, e := os.ReadFile(filepath.Join(dir, "transcript.json")); e == nil && json.Unmarshal(b, &transcript) == nil {
			for _, u := range transcript.Result.Utterances {
				if u.Additions.Speaker != "" {
					result.HasSpeakers = true
					break
				}
			}
		}
	}
	if library, e := loadVoiceLibrary(); e == nil {
		result.VoiceProfileCount = len(library.Voices)
		for name := range library.Voices {
			result.Names = append(result.Names, name)
			result.VoiceSampleCount += 1 + len(library.Samples[name])
		}
		sort.Strings(result.Names)
	}
	result.SpeakerGroups = []SpeakerGroup{}
	if _, _, groups, e := readSpeakerGroups(dir, s.VoiceThreshold); e == nil {
		result.SpeakerGroups = groups
	}
	return result, nil
}
func RecoverTranscriptJobs(root string) error {
	items, err := (meeting.Store{Root: root}).List()
	if err != nil {
		return err
	}
	for _, item := range items {
		dir := filepath.Join(root, item.Directory)
		runs, e := meeting.ListRuns(dir)
		if e != nil {
			return e
		}
		for _, run := range runs {
			changed := false
			if run.Automatic && !run.Closed {
				run.Closed = true
				run.State = "failed"
				if run.Error == "" {
					run.Error = "上次守候被中断，已完成的片段保留，可重试结束转写"
				}
				changed = true
			}
			if run.State == "queued" || run.State == "running" {
				run.State = "failed"
				run.Error = "上次转写被中断，可重试"
				changed = true
			}
			for i := range run.Clips {
				if run.Clips[i].State == "queued" || run.Clips[i].State == "running" {
					run.Clips[i].State = "failed"
					run.Clips[i].Error = "上次转写被中断，可重试"
					changed = true
				}
			}
			if changed {
				if e = meeting.SaveRun(dir, run); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

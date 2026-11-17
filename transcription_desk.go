package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
)

// enqueueDeskClip captures an immutable input before queueing. It never waits
// for ASR; the recorder can continue while any earlier clip is processed.
func (d *Desktop) enqueueDeskClip(id string, index int, clipPath string, retainedOffsetMS, sourceStartMS int64, s Settings) error {
	if index < 0 {
		return errors.New("Invalid speech clip index")
	}
	d.dataMu.Lock()
	store := meeting.Store{Root: s.Root}
	item, err := store.Find(id)
	if err != nil {
		d.dataMu.Unlock()
		return err
	}
	sessionDir := filepath.Join(s.Root, item.Directory)
	var run meeting.TranscriptRun
	if item.AutoRun != "" {
		run, err = meeting.LoadRun(sessionDir, item.AutoRun)
	} else {
		run, err = d.createRunLocked(item, s, true)
		if err == nil {
			item.AutoRun = run.ID
			err = store.Save(item)
		}
	}
	if err != nil {
		d.dataMu.Unlock()
		return err
	}
	for _, clip := range run.Clips {
		if clip.Index == index {
			d.dataMu.Unlock()
			return nil
		}
	}
	if run.Closed {
		d.dataMu.Unlock()
		return errors.New("对话已结束，不能追加录音")
	}
	dir, _ := meeting.RunDir(sessionDir, run.ID)
	work := filepath.Join(dir, "clips", fmt.Sprintf("%06d", index))
	if err = os.MkdirAll(work, 0700); err != nil {
		d.dataMu.Unlock()
		return err
	}
	input := "input" + filepath.Ext(clipPath)
	if filepath.Ext(clipPath) == "" {
		input = "input.wav"
	}
	if err = snapshotAudio(clipPath, filepath.Join(work, input), true); err != nil {
		d.dataMu.Unlock()
		return err
	}
	run.Clips = append(run.Clips, meeting.TranscriptClip{Index: index, Input: input, RetainedOffsetMS: retainedOffsetMS, SourceStartMS: sourceStartMS, State: "queued"})
	run.State = "queued"
	err = meeting.SaveRun(sessionDir, run)
	d.dataMu.Unlock()
	if err != nil {
		return err
	}
	d.addTranscriptionJob(transcriptionJob{s.Root, id, run.ID, index})
	return nil
}
func (d *Desktop) FinalizeDeskTranscription(id string, s Settings) error {
	d.dataMu.Lock()
	item, err := (meeting.Store{Root: s.Root}).Find(id)
	if err != nil {
		d.dataMu.Unlock()
		return err
	}
	if item.AutoRun == "" {
		d.dataMu.Unlock()
		return nil
	}
	sessionDir := filepath.Join(s.Root, item.Directory)
	run, err := meeting.LoadRun(sessionDir, item.AutoRun)
	if err == nil {
		run.Closed = true
		err = meeting.SaveRun(sessionDir, run)
	}
	d.dataMu.Unlock()
	if err != nil {
		return err
	}
	d.addTranscriptionJob(transcriptionJob{s.Root, id, run.ID, -2})
	return nil
}
func (d *Desktop) executeFinalization(ctx context.Context, job transcriptionJob) (err error) {
	defer func() {
		if err != nil {
			d.failTranscription(job, err)
		}
	}()
	d.dataMu.Lock()
	item, err := (meeting.Store{Root: job.Root}).Find(job.SessionID)
	if err != nil {
		d.dataMu.Unlock()
		return err
	}
	run, err := meeting.LoadRun(filepath.Join(job.Root, item.Directory), job.RunID)
	d.dataMu.Unlock()
	if err != nil {
		return err
	}
	var s Settings
	if err = json.Unmarshal(run.Settings, &s); err != nil {
		return err
	}
	s.Root = job.Root
	return d.finishTranscription(ctx, job, s, readKey(s.TitleEndpoint, "title"))
}

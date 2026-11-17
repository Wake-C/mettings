package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mettings.local/desktop/internal/meeting"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// launch is kept for recording/import callers; ASR ownership is transferred to
// its own serial queue so continuous capture is never gated by inference.
func (d *Desktop) launch(item meeting.Session, s Settings) {
	err := d.enqueueTranscription(item.ID, s, true)
	d.mu.Lock()
	d.busy = false
	d.cancel = nil
	if err != nil {
		d.phase = err.Error()
	}
	d.mu.Unlock()
}
func (d *Desktop) setPhase(phase string) { d.mu.Lock(); d.phase = phase; d.mu.Unlock() }

// pipeline is the synchronous checkpoint/resume entry point used by regression
// and native integration tests. Product actions use the persistent queue.
func (d *Desktop) pipeline(ctx context.Context, item meeting.Session, s Settings, keys meeting.Credentials, titleKey string) error {
	d.dataMu.Lock()
	store := meeting.Store{Root: s.Root}
	current, err := store.Find(item.ID)
	if err != nil {
		d.dataMu.Unlock()
		return err
	}
	dir := filepath.Join(s.Root, current.Directory)
	runs, err := meeting.ListRuns(dir)
	if err != nil {
		d.dataMu.Unlock()
		return err
	}
	if current.Summarized && len(runs) > 0 {
		d.dataMu.Unlock()
		return nil
	}
	var run meeting.TranscriptRun
	for _, candidate := range runs {
		if !candidate.Automatic && candidate.State == "failed" {
			run = candidate
			break
		}
	}
	if run.ID == "" {
		run, err = d.createRunLocked(current, s, false)
	}
	d.dataMu.Unlock()
	if err != nil {
		return err
	}
	return d.executeTranscription(ctx, transcriptionJob{s.Root, item.ID, run.ID, -1}, &transcriptionCredentials{ASR: keys, Title: titleKey})
}

type transcriptionCredentials struct {
	ASR   meeting.Credentials
	Title string
}

func (d *Desktop) executeTranscription(ctx context.Context, job transcriptionJob, explicit *transcriptionCredentials) (err error) {
	run, dir, err := d.updateRun(job, func(run *meeting.TranscriptRun) {
		run.State = "running"
		run.Error = ""
		if job.ClipIndex >= 0 {
			for i := range run.Clips {
				if run.Clips[i].Index == job.ClipIndex {
					run.Clips[i].State = "running"
					run.Clips[i].Error = ""
				}
			}
		}
	})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			d.failTranscription(job, err)
		}
	}()
	var s Settings
	if err = json.Unmarshal(run.Settings, &s); err != nil {
		return err
	}
	s.Root = job.Root
	keys := transcriptionCredentials{}
	if explicit != nil {
		keys = *explicit
	} else {
		if s.ASR.Kind != "local" {
			keys.ASR = meeting.Credentials{Key: readKey(s.ASR.Endpoint, "asr"), AppKey: readKey(s.ASR.Endpoint, "volc-app")}
		}
		keys.Title = readKey(s.TitleEndpoint, "title")
	}
	work := dir
	input := run.Input
	transcribed, polished := run.Transcribed, run.Polished
	var clip meeting.TranscriptClip
	if job.ClipIndex >= 0 {
		found := false
		for _, candidate := range run.Clips {
			if candidate.Index == job.ClipIndex {
				clip = candidate
				found = true
				break
			}
		}
		if !found {
			return errors.New("转写片段不存在")
		}
		work = filepath.Join(dir, "clips", fmt.Sprintf("%06d", clip.Index))
		input = clip.Input
		transcribed, polished = clip.Transcribed, clip.Polished
	}
	audio := filepath.Join(work, input)
	if transcribed {
		data, e := os.ReadFile(filepath.Join(work, "transcript.json"))
		if e != nil {
			return e
		}
		var cached meeting.Result
		if e = json.Unmarshal(data, &cached); e != nil {
			return e
		}
		if len(cached.Result.Utterances) == 0 && strings.TrimSpace(cached.Result.Text) == "" {
			return d.completeWithoutText(ctx, job, s, keys.Title, work, cached)
		}
	}
	checkpoint := func(stage string) error {
		_, _, e := d.updateRun(job, func(latest *meeting.TranscriptRun) {
			if job.ClipIndex < 0 {
				if stage == "transcribed" {
					latest.Transcribed = true
				} else {
					latest.Polished = true
				}
			} else {
				for i := range latest.Clips {
					if latest.Clips[i].Index == job.ClipIndex {
						if stage == "transcribed" {
							latest.Clips[i].Transcribed = true
						} else {
							latest.Clips[i].Polished = true
						}
					}
				}
			}
		})
		return e
	}
	if !transcribed {
		source := audio
		if s.ASR.Kind == "volc" || (s.ASR.Kind == "compatible" && filepath.Ext(audio) == ".bin") {
			source = filepath.Join(work, "asr.mp3")
			if exec.CommandContext(ctx, ffmpegPath(), "-y", "-i", audio, "-vn", "-ac", "1", "-ar", "16000", "-b:a", "64k", source).Run() != nil {
				return errors.New("无法准备转写音频")
			}
			defer os.Remove(source)
		}
		words := []string{}
		for _, word := range strings.Split(s.Hotwords, "\n") {
			word = strings.TrimSpace(word)
			if word != "" && !strings.HasPrefix(word, "#") {
				words = append(words, word)
			}
		}
		var result meeting.Result
		if s.ASR.Kind == "local" {
			result, err = localTranscribe(ctx, s, source, work)
		} else if s.ASR.Kind == "firered2-llm" {
			result, err = fireRedServiceTranscribe(ctx, s.ASR, keys.ASR, source, work)
		} else {
			result, err = meeting.Transcribe(ctx, s.ASR, keys.ASR, source, words)
		}
		if errors.Is(err, meeting.ErrNoTranscript) {
			return d.completeWithoutText(ctx, job, s, keys.Title, work, result)
		}
		if err != nil {
			return err
		}
		if result.DiarizationFallback {
			if _, _, err = d.updateRun(job, func(r *meeting.TranscriptRun) { r.Warning = "部分片段未能区分说话人" }); err != nil {
				return err
			}
		}
		if job.ClipIndex >= 0 {
			for i := range result.Result.Utterances {
				if speaker := result.Result.Utterances[i].Additions.Speaker; speaker != "" {
					result.Result.Utterances[i].Additions.Speaker = fmt.Sprintf("片段%d-%s", clip.Index, speaker)
				}
			}
		}
		timeline, e := readDeskTimeline(filepath.Dir(filepath.Dir(dir)))
		if e != nil {
			return e
		}
		stampDeskResult(&result, timeline, job.ClipIndex)
		if err = meeting.WriteJSON(filepath.Join(work, "transcript.json"), result); err != nil {
			return err
		}
		if len(result.Raw) > 0 {
			if err = os.WriteFile(filepath.Join(work, "provider-response.json"), result.Raw, 0600); err != nil {
				return err
			}
		}
		if err = os.WriteFile(filepath.Join(work, "transcript.md"), []byte(meeting.Markdown(result)), 0600); err != nil {
			return err
		}
		if err = checkpoint("transcribed"); err != nil {
			return err
		}
	}
	if !polished {
		degraded, e := attribute(ctx, s, work, audio)
		if e != nil {
			return e
		}
		if degraded {
			_, _, err = d.updateRun(job, func(latest *meeting.TranscriptRun) {
				latest.Warning = "部分说话人尚未识别。可播放片段并标注姓名。"
			})
			if err != nil {
				return err
			}
		}
		if err = checkpoint("polished"); err != nil {
			return err
		}
	}
	if job.ClipIndex >= 0 {
		if _, _, err = d.updateRun(job, func(latest *meeting.TranscriptRun) {
			for i := range latest.Clips {
				if latest.Clips[i].Index == job.ClipIndex {
					latest.Clips[i].State = "complete"
					latest.Clips[i].Error = ""
				}
			}
		}); err != nil {
			return err
		}
		if err = d.aggregateAutomatic(job); err != nil {
			return err
		}
	} else {
		if _, _, err = d.updateRun(job, func(latest *meeting.TranscriptRun) { latest.Transcribed = true; latest.Polished = true }); err != nil {
			return err
		}
	}
	return d.finishTranscription(ctx, job, s, keys.Title)
}
func fallbackTitle(md string) string {
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ">") {
			continue
		}
		if i := strings.Index(line, "**："); i >= 0 {
			line = line[i+len("**："):]
		}
		return meeting.SafeTitle(line)
	}
	return "会议"
}

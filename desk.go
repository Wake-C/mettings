package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mettings.local/desktop/internal/desk"
	"mettings.local/desktop/internal/meeting"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type DeskState struct {
	Active    bool   `json:"active"`
	Phase     string `json:"phase"`
	SpeechMS  int64  `json:"speechMS"`
	ElapsedMS int64  `json:"elapsedMS"`
	ClipCount int    `json:"clipCount"`
	Current   string `json:"current"`
	Error     string `json:"error"`
}
type deskCapture struct {
	started                            time.Time
	settings                           Settings
	spool                              string
	capture, detector                  *exec.Cmd
	input                              io.WriteCloser
	pcm                                io.ReadCloser
	done                               chan error
	stopping                           atomic.Bool
	sourceStarted                      atomic.Int64
	captureClock                       *deskCaptureClock
	stopOnce                           sync.Once
	sessionID                          string
	conversationStartMS, lastClipEndMS int64
	keptMS                             int64
	clips                              int
}
type captureEvent struct {
	Type    string `json:"type"`
	Path    string `json:"path"`
	AtMS    int64  `json:"at_ms"`
	Speech  bool   `json:"speech"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
}
type DeskClip struct {
	Index            int              `json:"index"`
	Path             string           `json:"path"`
	StartMS          int64            `json:"sourceStartMS"`
	EndMS            int64            `json:"sourceEndMS"`
	RetainedOffsetMS int64            `json:"retainedOffsetMS"`
	DurationMS       int64            `json:"durationMS"`
	StartedAt        string           `json:"startedAt"`
	TimeAnchors      []DeskTimeAnchor `json:"timeAnchors,omitempty"`
	TimestampSource  string           `json:"timestampSource,omitempty"`
}
type DeskTimeAnchor struct {
	OffsetMS   int64  `json:"offsetMS"`
	RecordedAt string `json:"recordedAt"`
}

type sourceClockReader struct {
	source    io.Reader
	clock     *atomic.Int64
	timing    *deskCaptureClock
	bytesRead int64
}

func (r *sourceClockReader) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	if n > 0 {
		if r.timing != nil {
			r.bytesRead += int64(n)
			if e := r.timing.waitSamples((r.bytesRead + 3) / 4); e != nil {
				return 0, e
			}
		}
		r.clock.CompareAndSwap(0, time.Now().UnixNano())
	}
	return n, err
}
func (r *deskCapture) sourceTime(ms int64) time.Time {
	if r.captureClock != nil {
		at, _ := r.captureClock.timeAt(ms)
		return at
	}
	origin := r.started
	if start := r.sourceStarted.Load(); start != 0 {
		origin = time.Unix(0, start)
	}
	return origin.Add(time.Duration(ms) * time.Millisecond)
}
func deskHelperPath() string { return filepath.Join(filepath.Dir(speechPath()), "mettings-desk") }
func (d *Desktop) StartDesk() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.record != nil || d.desk != nil || d.busy {
		return errors.New("请先结束当前录音或安装")
	}
	if !deskReady() {
		return errors.New("请先安装桌边守候的录音与语音检测组件")
	}
	base := filepath.Join(dataDir(), "capture")
	if err := os.MkdirAll(base, 0700); err != nil {
		return err
	}
	spool, err := os.MkdirTemp(base, "desk-")
	if err != nil {
		return err
	}
	s := d.settings
	r := &deskCapture{started: time.Now(), settings: s, spool: spool, done: make(chan error, 1)}
	r.captureClock = newDeskCaptureClock(r.started)
	r.capture = exec.Command(ffmpegPath(), "-hide_banner", "-loglevel", "info", "-nostats", "-copyts", "-f", "avfoundation", "-use_wallclock_as_timestamps", "1", "-i", ":"+s.Device, "-vn", "-af", "aresample=16000,ashowinfo", "-ac", "1", "-ar", "16000", "-f", "f32le", "pipe:1")
	r.capture.Stderr = &deskTimestampWriter{clock: r.captureClock}
	r.input, err = r.capture.StdinPipe()
	if err != nil {
		os.RemoveAll(spool)
		return err
	}
	r.pcm, err = r.capture.StdoutPipe()
	if err != nil {
		r.input.Close()
		os.RemoveAll(spool)
		return err
	}
	r.detector = exec.Command(deskHelperPath(), filepath.Join(runtimeDir(), "silero_vad.onnx"), spool)
	r.detector.Stdin = &sourceClockReader{source: r.pcm, clock: &r.sourceStarted, timing: r.captureClock}
	events, err := r.detector.StdoutPipe()
	if err != nil {
		r.input.Close()
		r.pcm.Close()
		os.RemoveAll(spool)
		return err
	}
	if err = r.detector.Start(); err != nil {
		r.input.Close()
		r.pcm.Close()
		os.RemoveAll(spool)
		return errors.New("语音检测组件启动失败，请重新安装桌边守候组件")
	}
	if err = r.capture.Start(); err != nil {
		r.input.Close()
		r.pcm.Close()
		_ = r.detector.Wait()
		os.RemoveAll(spool)
		return errors.New("麦克风启动失败，请检查设备和权限")
	}
	d.desk = r
	d.deskState = DeskState{Active: true, Phase: "正在连接麦克风"}
	d.phase = "桌边守候已开启"
	if d.tray != nil {
		d.tray.SetTitle("◉")
	}
	go d.runDesk(r, events)
	return nil
}
func (d *Desktop) runDesk(r *deskCapture, events io.ReadCloser) {
	scanner := bufio.NewScanner(events)
	scanner.Buffer(make([]byte, 4096), 256*1024)
	var failure error
	for scanner.Scan() {
		var event captureEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			failure = errors.New("语音检测组件返回了无效状态")
			break
		}
		if err := d.handleDeskEvent(r, event); err != nil {
			failure = err
			break
		}
	}
	if err := scanner.Err(); failure == nil && err != nil {
		failure = errors.New("语音检测数据流中断")
	}
	// A wrapped PCM reader makes os/exec copy stdin in a goroutine. On an
	// unexpected detector EOF, stop its upstream before Wait joins that reader.
	// During a requested stop, preserve the normal drain-and-flush order.
	if failure != nil || !r.stopping.Load() {
		_ = r.capture.Process.Kill()
	}
	if failure != nil {
		_ = r.detector.Process.Kill()
	}
	detectorErr := r.detector.Wait()
	if failure == nil && r.captureClock != nil {
		failure = r.captureClock.failure()
	}
	if !r.stopping.Load() {
		if failure == nil {
			failure = errors.New("录音设备或语音检测已停止，已保存的片段仍可使用")
		}
	}
	captureErr := r.capture.Wait()
	r.input.Close()
	r.pcm.Close()
	events.Close()
	if failure == nil && r.stopping.Load() && detectorErr != nil {
		failure = errors.New("语音检测未能正常结束，已保存片段保留")
	}
	if failure == nil && captureErr != nil {
		failure = errors.New("麦克风录音异常结束，已保存的片段保留")
	}
	if err := d.finishDeskConversation(r, failure); failure == nil && err != nil {
		failure = err
	}
	if failure == nil {
		_ = os.RemoveAll(r.spool)
	} // On failure keep any sealed, unimported clips for recovery.
	d.mu.Lock()
	if d.desk == r {
		d.desk = nil
	}
	d.deskState.Active = false
	d.deskState.ElapsedMS = time.Since(r.started).Milliseconds()
	d.deskState.Phase = "已暂停"
	if failure != nil {
		d.deskState.Error = failure.Error()
		d.deskState.Phase = "守候已停止"
	}
	d.phase = d.deskState.Phase
	d.mu.Unlock()
	if d.tray != nil {
		d.tray.SetTitle("")
	}
	r.done <- failure
	close(r.done)
}
func (d *Desktop) StopDesk() error {
	d.mu.Lock()
	r := d.desk
	if r != nil {
		d.deskState.Phase = "正在保存最后一段声音"
	}
	d.mu.Unlock()
	if r == nil {
		return nil
	}
	r.stopOnce.Do(func() { r.stopping.Store(true); _, _ = io.WriteString(r.input, "q\n"); r.input.Close() })
	select {
	case err := <-r.done:
		return err
	case <-time.After(15 * time.Second):
		_ = r.capture.Process.Kill()
	}
	select {
	case err := <-r.done:
		return err
	case <-time.After(5 * time.Second):
		_ = r.detector.Process.Kill()
		return <-r.done
	}
}
func (d *Desktop) handleDeskEvent(r *deskCapture, e captureEvent) error {
	switch e.Type {
	case "activity":
		// Quiet time changes the listening indicator, not the recording folder.
		// One Start owns one session until Pause/Stop or capture failure.
		d.mu.Lock()
		d.deskState.ElapsedMS = e.AtMS
		if !r.stopping.Load() {
			if e.Speech {
				d.deskState.Phase = "检测到交谈"
			} else {
				d.deskState.Phase = "听候中"
			}
		}
		d.mu.Unlock()
	case "clip":
		return d.saveDeskClip(r, e)
	}
	return nil
}
func (d *Desktop) saveDeskClip(r *deskCapture, e captureEvent) error {
	if r.captureClock != nil && r.sourceTime(e.StartMS).IsZero() {
		return errors.New("录音时间信息缺失，请重新开始守候；已保存的片段仍保留")
	}
	rel, err := filepath.Rel(r.spool, e.Path)
	if err != nil || !filepath.IsLocal(rel) {
		return errors.New("语音片段位置无效")
	}
	if e.EndMS <= e.StartMS {
		return errors.New("语音片段时间无效")
	}
	pcm, err := desk.PCM16(e.Path)
	if err != nil {
		return err
	}
	d.dataMu.Lock()
	store := meeting.Store{Root: r.settings.Root}
	var item meeting.Session
	if r.sessionID == "" {
		item, err = store.Create(r.sourceTime(e.StartMS))
		if err == nil {
			item.Mode = "desk"
			item.Active = true
			item.Audio = "recording.wav"
			r.sessionID = item.ID
			r.conversationStartMS = e.StartMS
		}
	} else {
		item, err = store.Find(r.sessionID)
	}
	if err != nil {
		d.dataMu.Unlock()
		return err
	}
	dir := filepath.Join(r.settings.Root, item.Directory)
	clipsDir := filepath.Join(dir, "clips")
	if err = os.MkdirAll(clipsDir, 0700); err != nil {
		d.dataMu.Unlock()
		return err
	}
	index := item.ClipCount + 1
	clipRel := fmt.Sprintf("clips/%06d.wav", index)
	clipPath := filepath.Join(dir, clipRel)
	if err = copySealedClip(e.Path, clipPath); err != nil {
		d.dataMu.Unlock()
		return err
	}
	offset, duration, err := desk.Append(filepath.Join(dir, item.Audio), pcm)
	if err != nil {
		d.dataMu.Unlock()
		return err
	}
	clips := []DeskClip{}
	if data, e := os.ReadFile(filepath.Join(dir, "clips.json")); e == nil {
		if err = json.Unmarshal(data, &clips); err != nil {
			d.dataMu.Unlock()
			return err
		}
	}
	clip := DeskClip{Index: index, Path: clipRel, StartMS: e.StartMS, EndMS: e.EndMS, RetainedOffsetMS: offset, DurationMS: duration, StartedAt: r.sourceTime(e.StartMS).Format(time.RFC3339Nano)}
	if r.captureClock != nil {
		clip.TimeAnchors = r.captureClock.clipAnchors(e.StartMS, e.EndMS)
		clip.TimestampSource = "capture-pts"
	}
	clips = append(clips, clip)
	if err = meeting.WriteJSON(filepath.Join(dir, "clips.json"), clips); err == nil {
		item.ClipCount = index
		item.SpeechMS = offset + duration
		item.ElapsedMS = e.EndMS - r.conversationStartMS
		err = store.Save(item)
	}
	d.dataMu.Unlock()
	if err != nil {
		return err
	}
	_ = os.Remove(e.Path)
	r.lastClipEndMS = e.EndMS
	r.keptMS += duration
	r.clips++
	d.mu.Lock()
	d.current = item.ID
	d.deskState.Current = item.ID
	d.deskState.SpeechMS = r.keptMS
	d.deskState.ClipCount = r.clips
	auto := d.settings.DeskAutoASR
	d.mu.Unlock()
	if auto {
		s := r.settings
		s.ASR.Kind = "local"
		if err = d.enqueueDeskClip(item.ID, index, clipPath, offset, e.StartMS, s); err != nil {
			d.mu.Lock()
			d.deskState.Error = "片段已保存，转写暂未开始：" + err.Error()
			d.mu.Unlock()
		}
	}
	return nil
}
func copySealedClip(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(target+".tmp", target)
}
func (d *Desktop) finishDeskConversation(r *deskCapture, cause error) error {
	if r.sessionID == "" {
		return nil
	}
	id := r.sessionID
	d.dataMu.Lock()
	store := meeting.Store{Root: r.settings.Root}
	item, err := store.Find(id)
	if err == nil {
		item.Active = false
		item.ElapsedMS = r.lastClipEndMS - r.conversationStartMS
		if cause != nil {
			item.Error = cause.Error()
			item.CaptureError = cause.Error()
		}
		err = store.Save(item)
	}
	d.dataMu.Unlock()
	if err != nil {
		return err
	}
	r.sessionID = ""
	s := r.settings
	s.ASR.Kind = "local"
	if err := d.FinalizeDeskTranscription(id, s); err != nil {
		d.mu.Lock()
		d.deskState.Error = "音频已保存，转写暂未完成：" + err.Error()
		d.mu.Unlock()
	}
	return nil
}
func recoverCaptures(root string) error {
	store := meeting.Store{Root: root}
	items, err := store.List()
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Active {
			item.Active = false
			item.Error = "录音被中断，已保存的音频仍可使用"
			item.CaptureError = item.Error
			if err = store.Save(item); err != nil {
				return err
			}
		}
	}
	return nil
}

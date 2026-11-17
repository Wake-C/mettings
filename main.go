package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"io"
	"io/fs"
	"mettings.local/desktop/internal/meeting"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed frontend/*
var frontend embed.FS

//go:embed assets/tray.png
var trayIcon []byte

type recording struct {
	cmd      *exec.Cmd
	input    io.WriteCloser
	done     chan error
	item     meeting.Session
	settings Settings
	started  time.Time
}
type Desktop struct {
	dataMu              sync.Mutex
	labelMu             sync.Mutex
	transcripts         *transcriptionQueue
	desk                *deskCapture
	deskState           DeskState
	setup               SetupState
	mu                  sync.Mutex
	settings            Settings
	window, preferences *mygo.Window
	tray                *mygo.Tray
	record              *recording
	playback            *exec.Cmd
	busy                bool
	phase               string
	current             string
	cancel              context.CancelFunc
	quitting            atomic.Bool
}
type Snapshot struct {
	Desk         DeskState         `json:"desk"`
	Queued       int               `json:"queued"`
	JobsRunning  bool              `json:"jobsRunning"`
	JobSessionID string            `json:"jobSessionID"`
	JobRunID     string            `json:"jobRunID"`
	Playing      bool              `json:"playing"`
	Root         string            `json:"root"`
	Sessions     []meeting.Session `json:"sessions"`
	Recording    bool              `json:"recording"`
	Busy         bool              `json:"busy"`
	Phase        string            `json:"phase"`
	Current      string            `json:"current"`
	Seconds      int               `json:"seconds"`
}
type Detail struct {
	TimestampEstimated bool             `json:"timestampEstimated,omitempty"`
	SpeakerGroups      []SpeakerGroup   `json:"speakerGroups"`
	VoiceProfileCount  int              `json:"voiceProfileCount"`
	VoiceSampleCount   int              `json:"voiceSampleCount"`
	HasSpeakers        bool             `json:"hasSpeakers"`
	RunID              string           `json:"runId"`
	Session            meeting.Session  `json:"session"`
	Transcript         string           `json:"transcript"`
	Unidentified       []map[string]any `json:"unidentified"`
	Names              []string         `json:"names"`
}

func main() {
	s, err := loadSettings()
	d := &Desktop{settings: s}
	if err != nil {
		d.phase = "设置读取失败，请打开设置重新保存"
	}
	mygo.App.SetName("Mettings")
	mygo.App.SetVersion("0.2.0")
	mygo.Theme.SetSource(mygo.ThemeLight)
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	_ = RecoverTranscriptJobs(s.Root)
	_ = recoverCaptures(s.Root)
	web, _ := fs.Sub(frontend, "frontend")
	mygo.SetFrontend(web)
	mygo.Bind(d)
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnActivate(func(bool) { d.Show() })
	mygo.App.OnSecondInstance(func([]string, string) { d.Show() })
	mygo.App.WhenReady(func() {
		d.window = mygo.NewWindow(mygo.WindowOptions{Title: "Mettings", URL: "/", Width: 1060, Height: 740, MinWidth: 720, MinHeight: 520, BackgroundColor: "#ffffff"})
		d.window.OnClose(func(e *mygo.CloseEvent) {
			if !d.quitting.Load() {
				e.PreventDefault()
				d.window.Hide()
			}
		})
		d.preferences = mygo.NewWindow(mygo.WindowOptions{Title: "Mettings 设置", URL: "/?view=settings", Width: 640, Height: 760, MinWidth: 500, MinHeight: 520, Hidden: true, BackgroundColor: "#ffffff"})
		d.preferences.OnClose(func(e *mygo.CloseEvent) {
			if !d.quitting.Load() {
				e.PreventDefault()
				d.preferences.Hide()
			}
		})
		d.tray, _ = mygo.NewTray(mygo.TrayOptions{ID: "com.mettings.desktop.tray", Icon: trayIcon, IconIsTemplate: true, ToolTip: "Mettings", Menu: mygo.NewMenu([]*mygo.MenuItem{
			{Label: "打开 Mettings", Click: func(*mygo.MenuItem, *mygo.Window) { d.Show() }},
			{Label: "开始录音", Click: func(*mygo.MenuItem, *mygo.Window) {
				go func() {
					if err := d.Start(); err != nil {
						d.setPhase(err.Error())
						d.Show()
					}
				}()
			}},
			{Label: "结束并保存", Click: func(*mygo.MenuItem, *mygo.Window) {
				go func() {
					if err := d.Finish(); err != nil {
						d.setPhase(err.Error())
						d.Show()
					}
				}()
			}},
			{Label: "开始桌边守候", Click: func(*mygo.MenuItem, *mygo.Window) {
				go func() {
					if err := d.StartDesk(); err != nil {
						d.setPhase(err.Error())
						d.Show()
					}
				}()
			}},
			{Label: "暂停桌边守候", Click: func(*mygo.MenuItem, *mygo.Window) {
				go func() {
					if err := d.StopDesk(); err != nil {
						d.setPhase(err.Error())
						d.Show()
					}
				}()
			}},
			{Label: "设置…", Click: func(*mygo.MenuItem, *mygo.Window) { d.OpenSettings() }}, {Role: mygo.RoleQuit},
		})})
	})
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if d.quitting.Load() {
			return
		}
		queue := d.TranscriptionStatus()
		d.mu.Lock()
		active := d.record != nil || d.desk != nil || d.busy || queue.Running || queue.Queued > 0
		d.mu.Unlock()
		if !active {
			d.quitting.Store(true)
			d.StopPlayback()
			return
		}
		e.PreventDefault()
		go func() {
			r, _ := mygo.Dialog.Message(mygo.MessageOptions{Type: mygo.MessageWarning, Title: "退出 Mettings", Message: "停止当前会话并退出？", Detail: "录音和已完成的步骤会保留。下次可继续转写。", Buttons: []string{"取消", "停止并退出"}, CancelButton: 0})
			if r.Button == 1 {
				_ = d.Stop(false)
				_ = d.StopDesk()
				d.CancelTranscription()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = d.WaitTranscriptions(ctx)
				cancel()
				d.mu.Lock()
				if d.cancel != nil {
					d.cancel()
				}
				d.mu.Unlock()
				d.StopPlayback()
				d.quitting.Store(true)
				mygo.App.Quit()
			}
		}()
	})
	if err := mygo.App.Run(); err != nil {
		os.Exit(1)
	}
}
func (d *Desktop) Show() {
	if d.window != nil {
		d.window.Show()
		d.window.Focus()
	}
}
func (d *Desktop) OpenSettings() {
	if d.preferences != nil {
		d.preferences.Show()
		d.preferences.Focus()
	}
}
func (d *Desktop) Snapshot() (Snapshot, error) {
	d.mu.Lock()
	s := Snapshot{Desk: d.deskState, Playing: d.playback != nil, Root: d.settings.Root, Recording: d.record != nil, Busy: d.busy, Phase: d.phase, Current: d.current}
	if d.record != nil {
		s.Seconds = int(time.Since(d.record.started).Seconds())
	}
	root := d.settings.Root
	if d.desk != nil {
		s.Desk.ElapsedMS = time.Since(d.desk.started).Milliseconds()
	}
	d.mu.Unlock()
	queue := d.TranscriptionStatus()
	s.Queued = queue.Queued
	s.JobsRunning = queue.Running
	s.JobSessionID = queue.SessionID
	s.JobRunID = queue.RunID
	var err error
	s.Sessions, err = (meeting.Store{Root: root}).List()
	return s, err
}
func (d *Desktop) Detail(id string) (Detail, error) { return d.TranscriptDetail(id, "") }
func (d *Desktop) Finish() error {
	d.mu.Lock()
	auto := d.settings.MeetingAutoASR
	d.mu.Unlock()
	return d.Stop(auto)
}
func (d *Desktop) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.record != nil || d.desk != nil || d.busy {
		return errors.New("A session is already running")
	}
	s := d.settings
	if !captureReady() {
		return errors.New("请先安装录音组件")
	}
	d.dataMu.Lock()
	item, err := (meeting.Store{Root: s.Root}).Create(time.Now())
	d.dataMu.Unlock()
	if err != nil {
		return err
	}
	item.Mode = "meeting"
	item.Active = true
	if err = (meeting.Store{Root: s.Root}).Save(item); err != nil {
		return err
	}
	cmd := exec.Command(ffmpegPath(), "-hide_banner", "-loglevel", "error", "-f", "avfoundation", "-i", ":"+s.Device, "-vn", "-c:a", "aac", "-b:a", "128k", "-y", filepath.Join(s.Root, item.Directory, item.Audio))
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		input.Close()
		item.Active = false
		item.Error = "录音组件启动失败"
		item.CaptureError = item.Error
		_ = (meeting.Store{Root: s.Root}).Save(item)
		return errors.New("录音组件启动失败，请在设置中重新安装")
	}
	rec := &recording{cmd: cmd, input: input, done: make(chan error, 1), item: item, settings: s, started: time.Now()}
	d.record = rec
	d.current = item.ID
	d.phase = "正在录音"
	if d.tray != nil {
		d.tray.SetTitle("●")
	}
	go func() {
		err := cmd.Wait()
		rec.done <- err
		close(rec.done)
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.record == rec {
			d.record = nil
			d.phase = "录音已停止，请检查麦克风权限和设备"
			rec.item.Active = false
			rec.item.ElapsedMS = time.Since(rec.started).Milliseconds()
			rec.item.CaptureError = d.phase
			rec.item.Error = d.phase
			_ = (meeting.Store{Root: s.Root}).Save(rec.item)
			if d.tray != nil {
				d.tray.SetTitle("")
			}
		}
	}()
	return nil
}
func (d *Desktop) Stop(transcribe bool) error {
	d.mu.Lock()
	rec := d.record
	if rec == nil {
		d.mu.Unlock()
		return errors.New("No recording is running")
	}
	d.record = nil
	d.busy = true
	d.phase = "正在保存录音"
	d.mu.Unlock()
	_, _ = io.WriteString(rec.input, "q\n")
	rec.input.Close()
	var err error
	select {
	case err = <-rec.done:
	case <-time.After(15 * time.Second):
		_ = rec.cmd.Process.Kill()
		<-rec.done
		err = errors.New("Recording did not stop cleanly")
	}
	if d.tray != nil {
		d.tray.SetTitle("")
	}
	if err != nil {
		d.mu.Lock()
		d.busy = false
		d.mu.Unlock()
		rec.item.Active = false
		rec.item.ElapsedMS = time.Since(rec.started).Milliseconds()
		rec.item.CaptureError = "录音失败，请检查麦克风权限、设备与音频文件"
		rec.item.Error = rec.item.CaptureError
		_ = (meeting.Store{Root: rec.settings.Root}).Save(rec.item)
		return errors.New(rec.item.Error)
	}
	d.dataMu.Lock()
	store := meeting.Store{Root: rec.settings.Root}
	item, e := store.Find(rec.item.ID)
	if e == nil {
		item.Active = false
		item.ElapsedMS = time.Since(rec.started).Milliseconds()
		e = store.Save(item)
	}
	d.dataMu.Unlock()
	if e != nil {
		d.mu.Lock()
		d.busy = false
		d.mu.Unlock()
		return e
	}
	if transcribe {
		d.launch(item, rec.settings)
	} else {
		d.mu.Lock()
		d.busy = false
		d.phase = "录音已保存"
		d.mu.Unlock()
	}
	return nil
}
func (d *Desktop) Retry(id string) error { return d.RetryTranscription(id) }
func (d *Desktop) ImportAudio() error {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "导入录音", Filters: []mygo.FileFilter{{Name: "音频", Extensions: []string{"m4a", "mp3", "wav", "ogg", "flac", "mp4", "aac"}}}})
	if err != nil || len(paths) == 0 {
		return err
	}
	d.mu.Lock()
	if d.busy || d.record != nil {
		d.mu.Unlock()
		return errors.New("Wait until the current session stops")
	}
	d.busy = true
	s := d.settings
	d.mu.Unlock()
	success := false
	defer func() {
		if !success {
			d.mu.Lock()
			d.busy = false
			d.mu.Unlock()
		}
	}()
	store := meeting.Store{Root: s.Root}
	d.dataMu.Lock()
	item, err := store.Create(time.Now())
	d.dataMu.Unlock()
	if err != nil {
		return err
	}
	item.Audio = "recording" + strings.ToLower(filepath.Ext(paths[0]))
	if err = store.Save(item); err != nil {
		return err
	}
	source, err := os.Open(paths[0])
	if err != nil {
		return err
	}
	defer source.Close()
	dest, err := os.OpenFile(filepath.Join(s.Root, item.Directory, item.Audio), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(dest, source)
	closeErr := dest.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	d.mu.Lock()
	d.current = item.ID
	d.mu.Unlock()
	success = true
	d.completeSavedAudio(item, s)
	return nil
}
func (d *Desktop) ChooseFolder() (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "选择会议归档目录", Directory: true, CreateDirectories: true})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}
func (d *Desktop) OpenFolder(id string) error {
	s := d.GetSettings()
	root := s.Root
	if id != "" {
		item, err := (meeting.Store{Root: root}).Find(id)
		if err != nil {
			return err
		}
		root = filepath.Join(root, item.Directory)
	}
	return exec.Command("/usr/bin/open", root).Run()
}
func (d *Desktop) Devices() ([]map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, ffmpegPath(), "-hide_banner", "-f", "avfoundation", "-list_devices", "true", "-i", "").CombinedOutput()
	items := []map[string]string{}
	audio := false
	pattern := regexp.MustCompile(`\[(\d+)\] (.+)$`)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "AVFoundation audio devices") {
			audio = true
			continue
		}
		if audio {
			if m := pattern.FindStringSubmatch(line); len(m) > 0 {
				items = append(items, map[string]string{"id": m[1], "name": m[2]})
			}
		}
	}
	if len(items) == 0 {
		return items, errors.New("No microphones found. Check ffmpeg and microphone permission")
	}
	return items, nil
}
func (d *Desktop) Play(id, speaker string) error {
	if speaker != "" {
		return d.PlayVersion(id, "", speaker)
	}
	s := d.GetSettings()
	item, err := (meeting.Store{Root: s.Root}).Find(id)
	if err != nil {
		return err
	}
	return d.playFile(filepath.Join(s.Root, item.Directory, item.Audio))
}
func (d *Desktop) playFile(path string) error {
	d.StopPlayback()
	d.mu.Lock()
	defer d.mu.Unlock()
	cmd := exec.Command("/usr/bin/afplay", path)
	if err := cmd.Start(); err != nil {
		return err
	}
	d.playback = cmd
	go func() {
		_ = cmd.Wait()
		d.mu.Lock()
		if d.playback == cmd {
			d.playback = nil
		}
		d.mu.Unlock()
	}()
	return nil
}
func (d *Desktop) StopPlayback() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.playback != nil {
		_ = d.playback.Process.Kill()
		d.playback = nil
	}
}
func (d *Desktop) Label(id, speaker, name string) error { return d.LabelVersion(id, "", speaker, name) }
func (d *Desktop) LabelVersion(id, runID, speaker, name string) error {
	return d.labelVersion(id, runID, speaker, name, embedVoice)
}
func (d *Desktop) labelVersion(id, runID, speaker, name string, embed func(context.Context, string) ([]float64, error)) error {
	s := d.GetSettings()
	item, dir, err := transcriptPath(s.Root, id, runID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if !d.labelMu.TryLock() {
		return errors.New("转写处理中，请稍后标注说话人")
	}
	defer d.labelMu.Unlock()
	if err = labelVoiceWithEmbed(ctx, s, dir, speaker, name, embed); err != nil {
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
	if updated, e := store.Find(id); e == nil && updated.PendingSpeakers == 0 && updated.Warning == "部分说话人尚未识别。可播放片段并标注姓名。" {
		updated.Warning = ""
		return store.Save(updated)
	}
	return nil
}
func (d *Desktop) PlayVersion(id, runID, speaker string) error {
	if speaker == "" {
		return d.Play(id, "")
	}
	s := d.GetSettings()
	_, dir, err := transcriptPath(s.Root, id, runID)
	if err != nil {
		return err
	}
	var entries []UnknownSpeaker
	b, err := os.ReadFile(filepath.Join(dir, "unidentified.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &entries); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Speaker == speaker {
			return d.playFile(filepath.Join(dir, entry.Segment))
		}
	}
	return errors.New("找不到说话人片段")
}
func (d *Desktop) completeSavedAudio(item meeting.Session, s Settings) {
	d.mu.Lock()
	d.busy = false
	d.phase = "录音已保存"
	d.mu.Unlock()
	if s.MeetingAutoASR {
		d.launch(item, s)
	}
}
func (d *Desktop) notify(err error) {
	d.mu.Lock()
	id := d.current
	d.mu.Unlock()
	d.notifyTranscription(id, err)
}
func (d *Desktop) notifyTranscription(id string, err error) {
	if d.window == nil {
		return
	}
	text := "转写稿已归档"
	if detail, e := d.Detail(id); e == nil && detail.Session.PendingSpeakers > 0 {
		text = fmt.Sprintf("转写稿已归档，%d 位说话人待确认。", detail.Session.PendingSpeakers)
	}
	if err != nil {
		text = "转写未完成，录音已保留。打开 Mettings 重试。"
	}
	n := mygo.NewNotification(mygo.NotificationOptions{Title: "Mettings", Body: text})
	n.OnClick(d.Show)
	_ = n.Show()
}

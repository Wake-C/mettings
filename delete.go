package main

import (
	"errors"
	"os/exec"
	"path/filepath"

	"github.com/egoist/mygo"
	"mettings.local/desktop/internal/meeting"
)

func (d *Desktop) DeleteSession(id string) (bool, error) {
	s := d.GetSettings()
	item, err := (meeting.Store{Root: s.Root}).Find(id)
	if err != nil {
		return false, err
	}
	answer, err := mygo.Dialog.Message(mygo.MessageOptions{Type: mygo.MessageWarning, Title: "删除录音", Message: "将这条录音移到废纸篓？", Detail: item.Title + "\n音频、转写版本及会议文件会一起移走。共享声纹保留。", Buttons: []string{"取消", "移到废纸篓"}, CancelButton: 0})
	if err != nil || answer.Button != 1 {
		return false, err
	}
	if err = d.deleteSession(s.Root, id, trashMeeting); err != nil {
		return false, err
	}
	return true, nil
}

func (d *Desktop) deleteSession(root, id string, trash func(string) error) error {
	if !d.labelMu.TryLock() {
		return errors.New("转写处理中，请完成后再删除")
	}
	defer d.labelMu.Unlock()
	d.dataMu.Lock()
	defer d.dataMu.Unlock()
	item, err := (meeting.Store{Root: root}).Find(id)
	if err != nil {
		return err
	}
	if item.Active {
		return errors.New("请先结束这条录音，再删除")
	}
	q := d.transcriptionQueue()
	q.mu.Lock()
	queued := q.current != nil && q.current.Root == root && q.current.SessionID == id
	for _, job := range q.jobs {
		queued = queued || job.Root == root && job.SessionID == id
	}
	q.mu.Unlock()
	if queued {
		return errors.New("这条录音正在等待或进行转写，请完成后再删除")
	}
	runs, err := meeting.ListRuns(filepath.Join(root, item.Directory))
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.State == "queued" || run.State == "running" {
			return errors.New("这条录音正在等待或进行转写，请完成后再删除")
		}
		for _, clip := range run.Clips {
			if clip.State == "queued" || clip.State == "running" {
				return errors.New("这条录音正在等待或进行转写，请完成后再删除")
			}
		}
	}
	d.StopPlayback()
	if err = trash(filepath.Join(root, item.Directory)); err != nil {
		return err
	}
	d.mu.Lock()
	if d.current == id {
		d.current = ""
	}
	d.mu.Unlock()
	return nil
}

const trashScript = `ObjC.import('Foundation');
function run(args) {
 const error = Ref();
 const ok = $.NSFileManager.defaultManager.trashItemAtURLResultingItemURLError($.NSURL.fileURLWithPath(args[0]), null, error);
 if (!ok) throw new Error('Unable to move recording to Trash');
}`

func trashMeeting(path string) error {
	if err := exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", trashScript, path).Run(); err != nil {
		return errors.New("未能移到废纸篓，录音仍保留。请检查文件夹权限后重试")
	}
	return nil
}

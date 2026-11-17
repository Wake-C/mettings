package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mettings.local/desktop/internal/meeting"
)

// RegenerateTitle is an explicit action: use current configuration and the
// published transcript, without rerunning ASR or changing transcript versions.
func (d *Desktop) RegenerateTitle(id string) error {
	s := d.GetSettings()
	if strings.TrimSpace(s.TitleEndpoint) == "" || strings.TrimSpace(s.TitleModel) == "" {
		return errors.New("请先在设置中配置标题服务并测试连接")
	}
	if !d.labelMu.TryLock() {
		return errors.New("转写处理中，请稍后生成标题")
	}
	defer d.labelMu.Unlock()
	d.dataMu.Lock()
	store := meeting.Store{Root: s.Root}
	item, err := store.Find(id)
	var transcript []byte
	if err == nil {
		transcript, err = os.ReadFile(filepath.Join(s.Root, item.Directory, "transcript.md"))
	}
	d.dataMu.Unlock()
	if err != nil || strings.TrimSpace(string(transcript)) == "" {
		return errors.New("暂无转写文本，请先转写录音")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	title, err := generateTitle(ctx, s, string(transcript), readKey(s.TitleEndpoint, "title"))
	if err != nil {
		return err
	}
	d.dataMu.Lock()
	defer d.dataMu.Unlock()
	item, err = store.Find(id)
	if err != nil {
		return err
	}
	item.Title = title
	item.TitleSource = "manual"
	if item.Warning == "标题服务不可用，已使用转写稿首句。" || item.Warning == "标题服务不可用，已保留现有标题。" {
		item.Warning = ""
	}
	if item.ActiveRun != "" {
		run, e := meeting.LoadRun(filepath.Join(s.Root, item.Directory), item.ActiveRun)
		if e != nil {
			return e
		}
		run.Title = title
		if run.Warning == "标题服务不可用，已使用转写稿首句。" || run.Warning == "标题服务不可用，已保留现有标题。" {
			run.Warning = ""
		}
		if e = meeting.SaveRun(filepath.Join(s.Root, item.Directory), run); e != nil {
			return e
		}
	}
	if item.Active {
		return store.Save(item)
	}
	_, err = store.Archive(item, title)
	return err
}

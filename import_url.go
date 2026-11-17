package main

import (
	"context"
	"errors"
	"mettings.local/desktop/internal/meeting"
	"path/filepath"
	"time"
)

func (d *Desktop) ImportURL(raw string) error {
	d.mu.Lock()
	if d.busy || d.record != nil {
		d.mu.Unlock()
		return errors.New("请先停止当前会话")
	}
	if !captureReady() {
		d.mu.Unlock()
		return errors.New("请先完成本机组件安装")
	}
	d.busy = true
	s := d.settings
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.phase = "正在下载音频"
	d.mu.Unlock()
	success := false
	defer func() {
		cancel()
		if !success {
			d.mu.Lock()
			d.busy = false
			d.cancel = nil
			d.phase = "音频导入未完成"
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
	name, err := meeting.DownloadAudio(ctx, raw, filepath.Join(s.Root, item.Directory))
	if err != nil {
		item.Error = err.Error()
		_ = store.Save(item)
		return err
	}
	item.Audio = name
	if err = store.Save(item); err != nil {
		return err
	}
	d.mu.Lock()
	d.current = item.ID
	d.mu.Unlock()
	success = true
	d.completeSavedAudio(item, s)
	return nil
}

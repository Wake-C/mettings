package main

import (
	"errors"
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
)

func (d *Desktop) RenameSession(id, title string) error {
	title = meeting.SafeTitle(title)
	if title == "" {
		return errors.New("请输入录音标题")
	}
	if !d.labelMu.TryLock() {
		return errors.New("转写处理中，请稍后重命名")
	}
	defer d.labelMu.Unlock()
	s := d.GetSettings()
	d.dataMu.Lock()
	defer d.dataMu.Unlock()
	store := meeting.Store{Root: s.Root}
	item, err := store.Find(id)
	if err != nil {
		return err
	}
	if item.Active {
		return errors.New("先结束当前录音或对话，再重命名")
	}
	name := item.ID + "-" + title
	if name != item.Directory {
		target := filepath.Join(s.Root, name)
		if _, e := os.Stat(target); e == nil {
			return errors.New("同名目录已存在")
		}
		if err = os.Rename(filepath.Join(s.Root, item.Directory), target); err != nil {
			return err
		}
		item.Directory = name
	}
	item.Title = title
	item.TitleSource = "manual"
	return store.Save(item)
}

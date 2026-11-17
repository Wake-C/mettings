package main

import (
	"encoding/json"
	"errors"
	"mettings.local/desktop/internal/meeting"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ArchiveEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
	Kind      string `json:"kind"`
}
type ArchiveView struct {
	Root    string         `json:"root"`
	Path    string         `json:"path"`
	Entries []ArchiveEntry `json:"entries"`
}

func archivePath(root, relative string) (string, error) {
	if relative != "" && !filepath.IsLocal(relative) {
		return "", errors.New("请选择会议目录中的文件")
	}
	target := filepath.Join(root, relative)
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, resolved)
	if err != nil || !filepath.IsLocal(rel) {
		return "", errors.New("该文件不在会议目录中")
	}
	return resolved, nil
}
func archiveKind(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".txt", ".json", ".srt", ".vtt":
		return "text"
	case ".m4a", ".mp3", ".wav", ".ogg", ".flac", ".aac", ".mp4", ".webm":
		return "audio"
	}
	return "file"
}
func (d *Desktop) BrowseArchive(relative string) (ArchiveView, error) {
	s := d.GetSettings()
	view := ArchiveView{Root: s.Root, Path: relative, Entries: []ArchiveEntry{}}
	path, err := archivePath(s.Root, relative)
	if os.IsNotExist(err) && relative == "" {
		return view, nil
	}
	if err != nil {
		return view, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return view, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			continue
		}
		kind := archiveKind(entry.Name())
		if info.IsDir() {
			kind = "folder"
		}
		view.Entries = append(view.Entries, ArchiveEntry{Name: entry.Name(), Path: filepath.Join(relative, entry.Name()), Directory: info.IsDir(), Size: info.Size(), Kind: kind})
	}
	sort.Slice(view.Entries, func(i, j int) bool {
		a, b := view.Entries[i], view.Entries[j]
		if a.Directory != b.Directory {
			return a.Directory
		}
		return a.Name > b.Name
	})
	return view, nil
}
func (d *Desktop) ReadArchiveFile(relative string) (string, error) {
	if archiveKind(relative) != "text" {
		return "", errors.New("此文件不能作为文本打开")
	}
	root := d.GetSettings().Root
	path, err := archivePath(root, relative)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err == nil && filepath.Base(path) == "transcript.md" {
		if sessionDir := deskSessionForPath(root, path); sessionDir != "" {
			if timeline, estimated, e := readDeskDisplayTimeline(sessionDir); e == nil && len(timeline) > 0 {
				var raw meeting.Result
				if data, e := os.ReadFile(filepath.Join(filepath.Dir(path), "transcript.json")); e == nil && json.Unmarshal(data, &raw) == nil {
					text := datedDeskMarkdown(string(b), timeline, raw.Result.Utterances)
					if estimated && !strings.Contains(text, meeting.EstimatedTimestampNotice) {
						text = meeting.EstimatedTimestampNotice + "\n\n" + text
					}
					return text, nil
				}
			}
		}
	}
	return string(b), err
}
func (d *Desktop) PlayArchiveFile(relative string) error {
	if archiveKind(relative) != "audio" {
		return errors.New("请选择音频文件")
	}
	path, err := archivePath(d.GetSettings().Root, relative)
	if err != nil {
		return err
	}
	return d.playFile(path)
}

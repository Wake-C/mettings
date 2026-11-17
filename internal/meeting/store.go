package meeting

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

type Session struct {
	CaptureError string `json:"captureError,omitempty"`
	Mode         string `json:"mode,omitempty"`
	Active       bool   `json:"active,omitempty"`
	SpeechMS     int64  `json:"speechMS,omitempty"`
	ElapsedMS    int64  `json:"elapsedMS,omitempty"`
	ClipCount    int    `json:"clipCount,omitempty"`
	ActiveRun    string `json:"activeRun,omitempty"`
	AutoRun      string `json:"autoRun,omitempty"`

	ID              string `json:"id"`
	Directory       string `json:"directory"`
	Title           string `json:"title"`
	TitleSource     string `json:"titleSource,omitempty"`
	TitleRunID      string `json:"titleRunId,omitempty"`
	StartedAt       string `json:"started_at"`
	Audio           string `json:"audio"`
	Transcribed     bool   `json:"transcribed"`
	Polished        bool   `json:"polished"`
	Summarized      bool   `json:"summarized"`
	Error           string `json:"error,omitempty"`
	Warning         string `json:"warning,omitempty"`
	PendingSpeakers int    `json:"pendingSpeakers"`
}
type Store struct{ Root string }

func WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
func (s Store) Create(now time.Time) (Session, error) {
	if err := os.MkdirAll(s.Root, 0700); err != nil {
		return Session{}, err
	}
	base := now.Format("2006-01-02-150405")
	existing, err := s.List()
	if err != nil {
		return Session{}, err
	}
	used := make(map[string]bool, len(existing))
	for _, item := range existing {
		used[item.ID] = true
	}
	for n := 0; n < 1000; n++ {
		id := base
		if n > 0 {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		if used[id] {
			continue
		}
		if err := os.Mkdir(filepath.Join(s.Root, id), 0700); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return Session{}, err
		}
		item := Session{ID: id, Directory: id, StartedAt: now.Format(time.RFC3339Nano), Audio: "recording.m4a"}
		return item, s.Save(item)
	}
	return Session{}, errors.New("Could not create a unique session folder")
}
func (s Store) Save(item Session) error {
	return WriteJSON(filepath.Join(s.Root, item.Directory, "status.json"), item)
}
func (s Store) List() ([]Session, error) {
	items := []Session{}
	entries, err := os.ReadDir(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return items, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var item Session
		b, err := os.ReadFile(filepath.Join(s.Root, entry.Name(), "status.json"))
		if err != nil || json.Unmarshal(b, &item) != nil {
			continue
		}
		if item.ID == "" {
			match := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}-\d{6}(?:-\d+)?)(?:-(.*))?$`).FindStringSubmatch(entry.Name())
			if match == nil {
				continue
			}
			item.ID, item.Title, item.Audio = match[1], match[2], "recording.m4a"
		}
		item.Directory = entry.Name()
		transcriptDir := filepath.Join(s.Root, entry.Name())
		if item.ActiveRun != "" {
			if path, e := RunDir(transcriptDir, item.ActiveRun); e == nil {
				transcriptDir = path
			}
		}
		item.PendingSpeakers = 0
		var voices []struct {
			Labeled bool   `json:"labeled"`
			Name    string `json:"name"`
		}
		if data, err := os.ReadFile(filepath.Join(transcriptDir, "unidentified.json")); err == nil && json.Unmarshal(data, &voices) == nil {
			for _, voice := range voices {
				if !voice.Labeled && voice.Name == "" {
					item.PendingSpeakers++
				}
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID > items[j].ID })
	return items, nil
}
func (s Store) Find(id string) (Session, error) {
	items, err := s.List()
	if err != nil {
		return Session{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return Session{}, errors.New("Session not found")
}
func SafeTitle(title string) string {
	title = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			return -1
		}
		return r
	}, title)
	title = strings.Join(strings.Fields(title), " ")
	rs := []rune(title)
	if len(rs) > 20 {
		title = string(rs[:20])
	}
	return strings.Trim(title, " .-")
}
func (s Store) Archive(item Session, title string) (Session, error) {
	title = SafeTitle(title)
	if title == "" {
		title = "会议-" + item.ID[:10]
	}
	name := item.ID + "-" + title
	if item.Directory != name {
		if _, err := os.Stat(filepath.Join(s.Root, name)); err == nil {
			return item, errors.New("Archive folder already exists")
		}
		if err := os.Rename(filepath.Join(s.Root, item.Directory), filepath.Join(s.Root, name)); err != nil {
			return item, err
		}
	}
	item.Directory = name
	item.Title = title
	item.Summarized = true
	return item, s.Save(item)
}

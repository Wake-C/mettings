package meeting

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// TranscriptRun is one immutable choice of model/settings. Its result checkpoints
// can advance on retry, but a new model always gets another run directory.
type TranscriptRun struct {
	ID               string           `json:"id"`
	Model            string           `json:"model"`
	Choice           string           `json:"choice"`
	State            string           `json:"state"`
	CreatedAt        string           `json:"createdAt"`
	UpdatedAt        string           `json:"updatedAt"`
	Error            string           `json:"error,omitempty"`
	Warning          string           `json:"warning,omitempty"`
	Automatic        bool             `json:"automatic"`
	Closed           bool             `json:"closed"`
	Transcribed      bool             `json:"transcribed"`
	Polished         bool             `json:"polished"`
	Summarized       bool             `json:"summarized"`
	Title            string           `json:"title,omitempty"`
	InitialTitleDone bool             `json:"initialTitleDone,omitempty"`
	Input            string           `json:"input,omitempty"`
	Settings         json.RawMessage  `json:"settings,omitempty"`
	Clips            []TranscriptClip `json:"clips,omitempty"`
}
type TranscriptClip struct {
	Index            int    `json:"index"`
	Input            string `json:"input"`
	RetainedOffsetMS int64  `json:"retainedOffsetMs"`
	SourceStartMS    int64  `json:"sourceStartMs"`
	State            string `json:"state"`
	Error            string `json:"error,omitempty"`
	Transcribed      bool   `json:"transcribed"`
	Polished         bool   `json:"polished"`
}

func RunDir(sessionDir, id string) (string, error) {
	if id == "" || filepath.Base(id) != id || !filepath.IsLocal(id) {
		return "", errors.New("Invalid transcript version")
	}
	return filepath.Join(sessionDir, "transcripts", id), nil
}
func CreateRun(sessionDir, model, choice string, settings json.RawMessage, automatic bool) (TranscriptRun, error) {
	parent := filepath.Join(sessionDir, "transcripts")
	if err := os.MkdirAll(parent, 0700); err != nil {
		return TranscriptRun{}, err
	}
	now := time.Now()
	base := now.Format("20060102-150405.000000000")
	for n := 0; n < 100; n++ {
		id := base
		if n > 0 {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		dir := filepath.Join(parent, id)
		if err := os.Mkdir(dir, 0700); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return TranscriptRun{}, err
		}
		run := TranscriptRun{ID: id, Model: model, Choice: choice, State: "queued", CreatedAt: now.Format(time.RFC3339Nano), Automatic: automatic, Closed: !automatic, Settings: settings}
		return run, SaveRun(sessionDir, run)
	}
	return TranscriptRun{}, errors.New("Could not create a transcript version")
}
func SaveRun(sessionDir string, run TranscriptRun) error {
	dir, err := RunDir(sessionDir, run.ID)
	if err != nil {
		return err
	}
	run.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	return WriteJSON(filepath.Join(dir, "run.json"), run)
}
func LoadRun(sessionDir, id string) (TranscriptRun, error) {
	var run TranscriptRun
	dir, err := RunDir(sessionDir, id)
	if err != nil {
		return run, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		return run, err
	}
	err = json.Unmarshal(b, &run)
	return run, err
}
func ListRuns(sessionDir string) ([]TranscriptRun, error) {
	runs := []TranscriptRun{}
	entries, err := os.ReadDir(filepath.Join(sessionDir, "transcripts"))
	if os.IsNotExist(err) {
		return runs, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		run, e := LoadRun(sessionDir, entry.Name())
		if e == nil {
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID > runs[j].ID })
	return runs, nil
}

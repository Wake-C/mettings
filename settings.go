package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"mettings.local/desktop/internal/meeting"
	"net"
	"os"
	"path/filepath"
	"strings"
)

type Settings struct {
	MeetingAutoASR bool             `json:"meetingAutoASR"`
	DeskAutoASR    bool             `json:"deskAutoASR"`
	LocalModel     string           `json:"localModel"`
	VoiceThreshold float64          `json:"voiceThreshold"`
	Root           string           `json:"root"`
	Device         string           `json:"device"`
	ASR            meeting.Provider `json:"asr"`
	TitleEndpoint  string           `json:"titleEndpoint"`
	TitleModel     string           `json:"titleModel"`
	Hotwords       string           `json:"hotwords"`
	Corrections    string           `json:"corrections"`
	Aliases        string           `json:"aliases"`
}

func dataDir() string {
	if path := os.Getenv("METTINGS_DATA_DIR"); path != "" {
		return path
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "Mettings")
}
func defaultSettings() Settings {
	home, _ := os.UserHomeDir()
	return Settings{LocalModel: "qwen3", VoiceThreshold: .6, Root: defaultArchive(home), Device: "default", ASR: meeting.Provider{Kind: "local", ResponseFormat: "json"}, Aliases: "{}"}
}
func defaultArchive(home string) string {
	legacy := filepath.Join(home, "Documents", "metting")
	if info, err := os.Stat(legacy); err == nil && info.IsDir() {
		return legacy
	}
	return filepath.Join(home, "Documents", "mettings")
}
func loadSettings() (Settings, error) {
	s := defaultSettings()
	b, err := os.ReadFile(filepath.Join(dataDir(), "settings.json"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	if s.Device == "" {
		s.Device = "default"
	}
	var saved map[string]json.RawMessage
	if err == nil && json.Unmarshal(b, &saved) == nil && saved["localModel"] == nil {
		s.LocalModel = "sensevoice"
	}
	return s, err
}
func identity(endpoint, role string) (string, string, error) {
	u, err := meeting.Endpoint(endpoint)
	if err != nil {
		return "", "", err
	}
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	digest := sha256.Sum256([]byte(strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)))
	return fmt.Sprintf("com.mettings.desktop.%s.%x", role, digest), os.Getenv("USER"), nil
}
func readKey(endpoint, role string) string {
	service, account, err := identity(endpoint, role)
	if err != nil {
		return ""
	}
	b, err := mygo.Secrets.Service(service).Get(account)
	if err != nil {
		return ""
	}
	return string(b)
}
func saveKey(endpoint, role, key string) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	service, account, err := identity(endpoint, role)
	if err != nil {
		return err
	}
	return mygo.Secrets.Service(service).Set(account, []byte(strings.TrimSpace(key)))
}
func (d *Desktop) GetSettings() Settings { d.mu.Lock(); defer d.mu.Unlock(); return d.settings }
func (d *Desktop) SaveSettings(s Settings, key, appKey, titleKey string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.busy || d.record != nil || d.desk != nil {
		return errors.New("Wait until the current session has stopped")
	}
	if !filepath.IsAbs(s.Root) {
		return errors.New("Choose an absolute archive folder path")
	}
	if s.ASR.Kind != "local" && s.ASR.Kind != "compatible" && s.ASR.Kind != "firered2-llm" && s.ASR.Kind != "volc" {
		return errors.New("Select a valid ASR provider")
	}
	if _, err := meeting.Endpoint(s.ASR.Endpoint); s.ASR.Kind != "local" && err != nil {
		return err
	}
	if (s.ASR.Kind == "compatible" || s.ASR.Kind == "firered2-llm") && strings.TrimSpace(s.ASR.Model) == "" {
		return errors.New("Enter an ASR model")
	}
	s.TitleEndpoint = strings.TrimSpace(s.TitleEndpoint)
	s.TitleModel = strings.TrimSpace(s.TitleModel)
	if (s.TitleEndpoint == "") != (s.TitleModel == "") {
		return errors.New("请同时填写标题服务地址和模型，或同时留空")
	}
	if s.TitleEndpoint != "" {
		if _, err := meeting.Endpoint(s.TitleEndpoint); err != nil {
			return err
		}
	}
	if _, ok := findLocalModel(s.LocalModel); !ok {
		return errors.New("请选择有效的本地语音模型")
	}
	var aliases map[string]string
	if s.VoiceThreshold <= 0 || s.VoiceThreshold > 1 {
		return errors.New("声纹匹配阈值必须大于 0 且不超过 1")
	}
	if json.Unmarshal([]byte(s.Aliases), &aliases) != nil {
		return errors.New("Aliases must be a JSON object mapping aliases to names")
	}
	if err := os.MkdirAll(dataDir(), 0700); err != nil {
		return err
	}
	for _, entry := range []struct{ endpoint, role, key string }{{s.ASR.Endpoint, "asr", key}, {s.ASR.Endpoint, "volc-app", appKey}, {s.TitleEndpoint, "title", titleKey}} {
		if err := saveKey(entry.endpoint, entry.role, entry.key); err != nil {
			return err
		}
	}
	if err := meeting.WriteJSON(filepath.Join(dataDir(), "settings.json"), s); err != nil {
		return err
	}
	for name, value := range map[string]string{"hotwords.txt": s.Hotwords, "fix-dict.txt": s.Corrections, "aliases.json": s.Aliases} {
		if err := os.WriteFile(filepath.Join(dataDir(), name), []byte(value), 0600); err != nil {
			return err
		}
	}
	d.settings = s
	return nil
}

func (d *Desktop) SelectLocalModel(model string) error {
	s := d.GetSettings()
	s.LocalModel = model
	return d.SaveSettings(s, "", "", "")
}

func (d *Desktop) SetRecordingOptions(mode string, auto bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.settings
	switch mode {
	case "meeting":
		s.MeetingAutoASR = auto
	case "desk":
		s.DeskAutoASR = auto
	default:
		return errors.New("请选择录音模式")
	}
	if err := os.MkdirAll(dataDir(), 0700); err != nil {
		return err
	}
	if err := meeting.WriteJSON(filepath.Join(dataDir(), "settings.json"), s); err != nil {
		return err
	}
	d.settings = s
	return nil
}

type ModelChoice struct {
	Tier          string `json:"tier,omitempty"`
	Description   string `json:"description,omitempty"`
	DownloadBytes int64  `json:"downloadBytes,omitempty"`
	ID            string `json:"id"`
	Label         string `json:"label"`
	Installed     bool   `json:"installed"`
}

func (d *Desktop) ModelChoices() []ModelChoice {
	s := d.GetSettings()
	choices := []ModelChoice{}
	for _, model := range localModels() {
		var size int64
		for _, part := range model.Parts {
			size += part.Bytes
		}
		choices = append(choices, ModelChoice{ID: model.ID, Label: model.Label, Tier: model.Tier, Description: model.Description, DownloadBytes: size, Installed: runtimeReady(model.ID)})
	}
	if s.ASR.Kind != "local" {
		label := modelLabel(s)
		choices = append(choices, ModelChoice{ID: "configured", Label: label, Installed: captureReady()})
	}
	return choices
}

// Model installation applies only the explicit model choice, not unsaved form fields.
func (d *Desktop) InstallLocalModel(model string) error {
	if err := d.SelectLocalModel(model); err != nil {
		return err
	}
	return d.InstallRuntime()
}

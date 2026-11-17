package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecommendedDefaultsUseSystemMicAndExplicitASR(t *testing.T) {
	s := defaultSettings()
	if s.Device != "default" || s.LocalModel != "qwen3" || s.ASR.Kind != "local" || s.MeetingAutoASR || s.DeskAutoASR || s.VoiceThreshold != .6 || s.TitleEndpoint != "" {
		t.Fatalf("unexpected defaults %+v", s)
	}
}
func TestSavedMicrophoneAndAdvancedChoicesRemain(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	os.WriteFile(filepath.Join(dataDir(), "settings.json"), []byte(`{"device":"3","localModel":"sensevoice","meetingAutoASR":true,"voiceThreshold":0.75,"aliases":"{\"小王\":\"王明\"}","asr":{"kind":"compatible","endpoint":"https://asr.example/v1","model":"my-model"}}`), 0600)
	s, err := loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Device != "3" || s.LocalModel != "sensevoice" || !s.MeetingAutoASR || s.VoiceThreshold != .75 || s.ASR.Model != "my-model" {
		t.Fatalf("saved choices changed %+v", s)
	}
}

func TestInstallActionAppliesTheSelectedModel(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	for _, model := range localModels() {
		for _, part := range componentsForModel(model.ID) {
			for _, rel := range part.Files {
				path := filepath.Join(runtimeDir(), rel)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(runtimeDir(), "."+part.ID+".installed"), []byte(part.SHA), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	s := defaultSettings()
	s.Root = t.TempDir()
	s.Hotwords = "保留的词语"
	d := &Desktop{settings: s}
	if err := d.InstallLocalModel("firered2"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for d.SetupStatus().Installing {
		if time.Now().After(deadline) {
			t.Fatal("installation did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	saved, err := loadSettings()
	if err != nil || saved.LocalModel != "firered2" || saved.Hotwords != s.Hotwords {
		t.Fatalf("wrong installation choice %+v %v", saved, err)
	}
	if d.SetupStatus().Error != "" {
		t.Fatal(d.SetupStatus().Error)
	}
}

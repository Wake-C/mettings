package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestQwenIsDefaultAndInstallsOnlySelectedASR(t *testing.T) {
	if defaultSettings().LocalModel != "qwen3" {
		t.Fatal("Qwen should be default")
	}
	for _, model := range []string{"qwen3", "sensevoice"} {
		seen := map[string]bool{}
		for _, c := range componentsForModel(model) {
			seen[c.ID] = true
		}
		if !seen[model] || seen[map[string]string{"qwen3": "sensevoice", "sensevoice": "qwen3"}[model]] {
			t.Fatalf("wrong ASR components %v", seen)
		}
		if !seen["ffmpeg"] || !seen["campplus"] || !seen["segmentation"] {
			t.Fatal("shared components missing")
		}
	}
}
func TestLegacySettingsKeepSenseVoice(t *testing.T) {
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	os.WriteFile(filepath.Join(dataDir(), "settings.json"), []byte(`{"asr":{"kind":"local"},"aliases":"{}"}`), 0600)
	s, err := loadSettings()
	if err != nil || s.LocalModel != "sensevoice" {
		t.Fatalf("legacy model changed: %+v %v", s, err)
	}
	s.LocalModel = "qwen3"
	b, _ := json.Marshal(s)
	os.WriteFile(filepath.Join(dataDir(), "settings.json"), b, 0600)
	s, err = loadSettings()
	if err != nil || s.LocalModel != "qwen3" {
		t.Fatal("explicit Qwen choice not retained")
	}
}

func TestExpandedModelCatalogIsSelectableAndHasPinnedArtifacts(t *testing.T) {
	for _, id := range []string{"sensevoice", "qwen3", "firered2", "qwen3-1.7b"} {
		model, ok := findLocalModel(id)
		if !ok || model.Label == "" || model.Directory == "" || model.Kind == "" {
			t.Fatalf("missing model %s", id)
		}
		s, err := settingsForChoice(defaultSettings(), id)
		if err != nil || s.LocalModel != id || s.ASR.Kind != "local" {
			t.Fatalf("unselectable %s %v", id, err)
		}
		if modelLabel(s) != model.Label {
			t.Fatalf("wrong history label for %s", id)
		}
		for _, part := range model.Parts {
			if len(part.SHA) != 64 || part.URL == "" || part.Bytes <= 0 {
				t.Fatalf("unpinned model artifact %s", part.ID)
			}
		}
	}
	if runtimeReady("unsupported") {
		t.Fatal("unknown model reported ready")
	}
}

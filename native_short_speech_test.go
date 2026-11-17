package main

import (
	"encoding/json"
	"errors"
	"mettings.local/desktop/internal/desk"
	"mettings.local/desktop/internal/meeting"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeShortSpeechDoesNotRequireSpeakerSegments(t *testing.T) {
	fixtures := os.Getenv("METTINGS_NATIVE_FIXTURES")
	if fixtures == "" {
		t.Skip("set public native fixtures")
	}
	helper := os.Getenv("METTINGS_SPEECH_HELPER")
	if helper == "" {
		helper = speechPath()
	}
	wave := filepath.Join(t.TempDir(), "syllable.wav")
	source := filepath.Join(fixtures, "models", localModelDirectory("sensevoice"), "test_wavs", "zh.wav")
	ffmpeg := filepath.Join(fixtures, "ffmpeg", "ffmpeg")
	if err := exec.Command(ffmpeg, "-loglevel", "error", "-y", "-ss", "1", "-i", source, "-t", "0.2", "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wave).Run(); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(helper, "transcribe", "firered2", filepath.Join(fixtures, "models", fireRedDirectory), filepath.Join(fixtures, "campplus.onnx"), filepath.Join(fixtures, "models", "sherpa-onnx-pyannote-segmentation-3-0", "model.onnx"), "/dev/null", wave).Output()
	if err != nil {
		t.Fatal(err)
	}
	var result meeting.Result
	if json.Unmarshal(output, &result) != nil {
		t.Fatal("invalid native result")
	}
	if len(result.Result.Utterances) == 0 || strings.TrimSpace(result.Result.Utterances[0].Text) == "" {
		t.Fatal("short audible speech was discarded before ASR")
	}
	if result.Result.Utterances[0].Additions.Speaker != "" {
		t.Fatal("fallback invented a speaker identity")
	}
}

func TestNativeSilenceDoesNotProduceFallbackWords(t *testing.T) {
	fixtures := os.Getenv("METTINGS_NATIVE_FIXTURES")
	if fixtures == "" {
		t.Skip("set public native fixtures")
	}
	wave := filepath.Join(t.TempDir(), "silence.wav")
	if _, _, err := desk.Append(wave, make([]byte, 2*32000)); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(speechPath(), "transcribe", "firered2", filepath.Join(fixtures, "models", fireRedDirectory), filepath.Join(fixtures, "campplus.onnx"), filepath.Join(fixtures, "models", "sherpa-onnx-pyannote-segmentation-3-0", "model.onnx"), "/dev/null", wave).Output()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = parseLocalTranscript(output); !errors.Is(err, meeting.ErrNoTranscript) {
		t.Fatalf("silence was not a no-text result: %v", err)
	}
}

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A captured container can have timestamp gaps. Speaker offsets refer to the
// decoded PCM used by ASR, not the container's original presentation timeline.
func TestVoiceClipUsesDecodedASRTimeline(t *testing.T) {
	fixtures := os.Getenv("METTINGS_NATIVE_FIXTURES")
	if fixtures == "" {
		t.Skip("set METTINGS_NATIVE_FIXTURES for real FFmpeg")
	}
	t.Setenv("METTINGS_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(ffmpegPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(fixtures, "ffmpeg", "ffmpeg"), ffmpegPath()); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "gapped.m4a")
	if out, err := exec.Command(ffmpegPath(), "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=16000:duration=4", "-af", "aselect=not(between(t\\,1\\,2))", "-c:a", "aac", source).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	target := filepath.Join(dir, "clip.wav")
	if err := wav(context.Background(), source, target, 500, 2500); err != nil {
		t.Fatal(err)
	}
	pcm, err := exec.Command(ffmpegPath(), "-v", "error", "-i", target, "-f", "s16le", "-ac", "1", "-ar", "16000", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 2*16000*2 {
		t.Fatalf("clip has %d PCM bytes; expected 64000 for selected 2s of decoded audio", len(pcm))
	}
}

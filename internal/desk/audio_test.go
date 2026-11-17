package desk

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestRetainedAudioAppendIsPlayableAndPreservesBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.wav")
	a := bytes.Repeat([]byte{1, 2}, 16000)
	b := bytes.Repeat([]byte{3, 4}, 8000)
	start, duration, err := Append(path, a)
	if err != nil || start != 0 || duration != 1000 {
		t.Fatalf("first clip %d %d %v", start, duration, err)
	}
	start, duration, err = Append(path, b)
	if err != nil || start != 1000 || duration != 500 {
		t.Fatalf("second clip %d %d %v", start, duration, err)
	}
	got, err := PCM16(path)
	if err != nil || !bytes.Equal(got, append(a, b...)) {
		t.Fatal("retained samples lost or duplicated", err)
	}
}

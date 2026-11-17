package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type deskNativeEvent struct {
	Type        string `json:"type"`
	Path        string `json:"path"`
	AtMS        int64  `json:"at_ms"`
	Speech      bool   `json:"speech"`
	StartSample int64  `json:"start_sample"`
	EndSample   int64  `json:"end_sample"`
	SampleRate  int    `json:"sample_rate"`
}

func readDeskTestWave(path string) ([]float32, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not WAV")
	}
	var pcm []byte
	validFormat := false
	for offset := 12; offset+8 <= len(b); {
		size := int(binary.LittleEndian.Uint32(b[offset+4 : offset+8]))
		start := offset + 8
		end := start + size
		if end > len(b) {
			return nil, fmt.Errorf("truncated WAV")
		}
		switch string(b[offset : offset+4]) {
		case "fmt ":
			if size >= 16 {
				validFormat = binary.LittleEndian.Uint16(b[start:]) == 1 && binary.LittleEndian.Uint16(b[start+2:]) == 1 && binary.LittleEndian.Uint32(b[start+4:]) == 16000 && binary.LittleEndian.Uint16(b[start+14:]) == 16
			}
		case "data":
			pcm = b[start:end]
		}
		offset = end + size%2
	}
	if !validFormat || len(pcm)%2 != 0 {
		return nil, fmt.Errorf("expected mono PCM16 at 16 kHz")
	}
	result := make([]float32, len(pcm)/2)
	for i := range result {
		result[i] = float32(int16(binary.LittleEndian.Uint16(pcm[2*i:]))) / 32768
	}
	return result, nil
}

type deskFragmentReader struct{ io.Reader }

func (r deskFragmentReader) Read(p []byte) (int, error) {
	if len(p) > 13 {
		p = p[:13]
	}
	return r.Reader.Read(p)
}

func runDeskNative(t *testing.T, source []float32, fragmented ...bool) []deskNativeEvent {
	t.Helper()
	fixtures := os.Getenv("METTINGS_NATIVE_FIXTURES")
	if fixtures == "" {
		t.Skip("set METTINGS_NATIVE_FIXTURES for real speech-detector tests")
	}
	helper := os.Getenv("METTINGS_DESK_HELPER")
	if helper == "" {
		helper = "build/Mettings.app/Contents/Resources/engine/mettings-desk"
	}
	model := filepath.Join(fixtures, "silero_vad.onnx")
	if _, err := os.Stat(helper); err != nil {
		t.Fatalf("build native desk helper first: %v", err)
	}
	data := make([]byte, len(source)*4)
	for i, sample := range source {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(sample))
	}
	spool := t.TempDir()
	cmd := exec.Command(helper, model, spool)
	cmd.Stdin = bytes.NewReader(data)
	if len(fragmented) > 0 && fragmented[0] {
		cmd.Stdin = deskFragmentReader{bytes.NewReader(data)}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("native desk failed: %v: %s", err, stderr.String())
	}
	events := []deskNativeEvent{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var e deskNativeEvent
		if err = json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("invalid JSON event: %s", line)
		}
		events = append(events, e)
	}
	lastEnd := int64(0)
	lastActivity := int64(0)
	for _, e := range events {
		switch e.Type {
		case "activity":
			if e.AtMS < lastActivity || e.AtMS > int64(len(source))*1000/16000 {
				t.Fatal("activity clock outside source")
			}
			lastActivity = e.AtMS
		case "clip":
			if e.SampleRate != 16000 || e.StartSample < lastEnd || e.EndSample > int64(len(source)) || e.EndSample <= e.StartSample {
				t.Fatalf("invalid or overlapping range: %+v", e)
			}
			if e.EndSample-e.StartSample > 30*16000 {
				t.Fatal("clip exceeded 30 seconds")
			}
			if !filepath.IsAbs(e.Path) || filepath.Dir(e.Path) != spool {
				t.Fatal("clip escaped spool")
			}
			clip, err := readDeskTestWave(e.Path)
			if err != nil {
				t.Fatal(err)
			}
			if int64(len(clip)) != e.EndSample-e.StartSample {
				t.Fatal("file length does not match event")
			}
			for i, sample := range clip {
				if math.Abs(float64(sample-source[int(e.StartSample)+i])) > 1.0/32768 {
					t.Fatalf("lost/reordered source sample at %d", int(e.StartSample)+i)
				}
			}
			lastEnd = e.EndSample
		default:
			t.Fatalf("unknown capture event %q", e.Type)
		}
	}
	files, _ := os.ReadDir(spool)
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".part") {
			t.Fatal("unfinished file left after EOF")
		}
	}
	return events
}

func deskTestSpeech(t *testing.T) []float32 {
	t.Helper()
	fixtures := os.Getenv("METTINGS_NATIVE_FIXTURES")
	if fixtures == "" {
		t.Skip("set METTINGS_NATIVE_FIXTURES for real speech-detector tests")
	}
	sample := filepath.Join(fixtures, "models", "sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2025-09-09", "test_wavs", "zh.wav")
	speech, err := readDeskTestWave(sample)
	if err != nil {
		t.Fatal(err)
	}
	return speech
}
func deskClips(events []deskNativeEvent) []deskNativeEvent {
	var clips []deskNativeEvent
	for _, e := range events {
		if e.Type == "clip" {
			clips = append(clips, e)
		}
	}
	return clips
}

func TestNativeDeskSilenceAndStationaryNoiseMakeNoClips(t *testing.T) {
	// Ninety seconds of quiet includes low stationary noise, not voice.
	source := make([]float32, 90*16000)
	seed := uint32(99)
	for i := range source {
		seed = 1664525*seed + 1013904223
		source[i] = (float32(seed>>8)/float32(1<<24) - .5) * .004
	}
	events := runDeskNative(t, source)
	if len(deskClips(events)) != 0 {
		t.Fatal("non-speech was archived")
	}
	if len(events) < 90 || events[len(events)-1].Speech || events[len(events)-1].AtMS != 90000 {
		t.Fatal("idle/EOF activity state missing")
	}
}
func TestNativeDeskDropsLongIdleButKeepsSpeechAndContext(t *testing.T) {
	speech := deskTestSpeech(t)
	source := make([]float32, 10*16000)
	source = append(source, speech...)
	source = append(source, make([]float32, 18*16000)...)
	source = append(source, speech...)
	source = append(source, make([]float32, 10*16000)...)
	clips := deskClips(runDeskNative(t, source))
	if len(clips) != 2 {
		t.Fatalf("expected two speech clips, got %d", len(clips))
	}
	if clips[0].StartSample > 10*16000 || clips[0].StartSample < 8*16000 {
		t.Fatal("opening pre-roll lost or idle preserved")
	}
	if clips[1].StartSample-clips[0].EndSample < 12*16000 {
		t.Fatal("long idle gap was not removed")
	}
	var kept int64
	for _, c := range clips {
		kept += c.EndSample - c.StartSample
	}
	if kept >= int64(len(source))/2 {
		t.Fatal("too much idle audio retained")
	}
}
func TestNativeDeskEOFInsideSpeechPreservesLastSamples(t *testing.T) {
	speech := deskTestSpeech(t)
	source := append(make([]float32, 3*16000), speech[:4*16000+123]...)
	clips := deskClips(runDeskNative(t, source))
	if len(clips) == 0 || clips[len(clips)-1].EndSample != int64(len(source)) {
		t.Fatal("EOF truncated active speech")
	}
}
func TestNativeDeskLongSpeechSplitsWithoutDuplicateOrMissingSamples(t *testing.T) {
	speech := deskTestSpeech(t)
	body := speech[16000 : 5*16000]
	var source []float32
	for i := 0; i < 17; i++ {
		source = append(source, body...)
	}
	clips := deskClips(runDeskNative(t, source))
	if len(clips) < 3 {
		t.Fatalf("long speech was not bounded: %d clips", len(clips))
	}
	if clips[0].StartSample != 0 || clips[len(clips)-1].EndSample != int64(len(source)) {
		t.Fatal("long speech boundary lost")
	}
	for i := 1; i < len(clips); i++ {
		if clips[i].StartSample != clips[i-1].EndSample {
			t.Fatal("continuous speech has gap or overlap")
		}
	}
}

func TestNativeDeskEmptyEOFReportsStoppedWithoutClip(t *testing.T) {
	events := runDeskNative(t, nil)
	if len(events) != 1 || events[0].Type != "activity" || events[0].AtMS != 0 || events[0].Speech {
		t.Fatalf("empty EOF state: %+v", events)
	}
}
func TestNativeDeskFragmentedPCMMatchesWholeInput(t *testing.T) {
	speech := deskTestSpeech(t)
	whole := runDeskNative(t, speech)
	fragments := runDeskNative(t, speech, true)
	if len(whole) != len(fragments) {
		t.Fatal("PCM write boundaries changed capture events")
	}
	for i := range whole {
		a, b := whole[i], fragments[i]
		a.Path = ""
		b.Path = ""
		if a != b {
			t.Fatalf("fragment changed event %d: %+v != %+v", i, a, b)
		}
	}
}
func TestNativeDeskMalformedEOFKeepsEarlierClips(t *testing.T) {
	speech := deskTestSpeech(t)
	body := speech[16000 : 5*16000]
	var source []float32
	for i := 0; i < 17; i++ {
		source = append(source, body...)
	}
	data := make([]byte, len(source)*4+1)
	for i, sample := range source {
		binary.LittleEndian.PutUint32(data[4*i:], math.Float32bits(sample))
	}
	data[len(data)-1] = 1
	fixtures := os.Getenv("METTINGS_NATIVE_FIXTURES")
	helper := os.Getenv("METTINGS_DESK_HELPER")
	if helper == "" {
		helper = "build/Mettings.app/Contents/Resources/engine/mettings-desk"
	}
	spool := t.TempDir()
	cmd := exec.Command(helper, filepath.Join(fixtures, "silero_vad.onnx"), spool)
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil || !strings.Contains(stderr.String(), "Incomplete float32 PCM input") {
		t.Fatalf("malformed EOF was not rejected: %v %s", err, stderr.String())
	}
	clips := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var event deskNativeEvent
		if json.Unmarshal([]byte(line), &event) != nil {
			t.Fatal("malformed earlier event")
		}
		if event.Type == "clip" {
			clips++
			if _, err := readDeskTestWave(event.Path); err != nil {
				t.Fatal("completed clip damaged")
			}
		}
	}
	if clips < 2 {
		t.Fatal("interruption did not preserve earlier bounded clips")
	}
	files, _ := os.ReadDir(spool)
	if len(files) != clips {
		t.Fatal("partial/unreported clip remained after interruption")
	}
}
func TestNativeDeskFFmpegSIGTERMFlushesActiveSpeech(t *testing.T) {
	_ = deskTestSpeech(t) // Checks fixture availability before starting native tools.
	fixtures := os.Getenv("METTINGS_NATIVE_FIXTURES")
	helper := os.Getenv("METTINGS_DESK_HELPER")
	if helper == "" {
		helper = "build/Mettings.app/Contents/Resources/engine/mettings-desk"
	}
	sample := filepath.Join(fixtures, "models", "sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2025-09-09", "test_wavs", "zh.wav")
	ffmpeg := exec.Command(filepath.Join(fixtures, "ffmpeg", "ffmpeg"), "-hide_banner", "-loglevel", "error", "-re", "-i", sample, "-vn", "-ac", "1", "-ar", "16000", "-f", "f32le", "pipe:1")
	pcm, err := ffmpeg.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pcm.Close()
	spool := t.TempDir()
	capture := exec.Command(helper, filepath.Join(fixtures, "silero_vad.onnx"), spool)
	capture.Stdin = pcm
	events, err := capture.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	capture.Stderr = &stderr
	if err = capture.Start(); err != nil {
		t.Fatal(err)
	}
	defer capture.Process.Kill()
	if err = ffmpeg.Start(); err != nil {
		t.Fatal(err)
	}
	defer ffmpeg.Process.Kill()
	var timedOut atomic.Bool
	timer := time.AfterFunc(10*time.Second, func() { timedOut.Store(true); _ = ffmpeg.Process.Kill(); _ = capture.Process.Kill() })
	defer timer.Stop()
	stopped := false
	var received []deskNativeEvent
	scanner := bufio.NewScanner(events)
	for scanner.Scan() {
		var event deskNativeEvent
		if err = json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		received = append(received, event)
		if !stopped && event.Type == "activity" && event.Speech && event.AtMS >= 2000 {
			if err = ffmpeg.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			stopped = true
		}
	}
	_ = ffmpeg.Wait()
	err = capture.Wait()
	if timedOut.Load() || err != nil || scanner.Err() != nil {
		t.Fatalf("capture did not flush on upstream SIGTERM: %v %v %s", err, scanner.Err(), stderr.String())
	}
	clips := deskClips(received)
	if !stopped || len(clips) == 0 {
		t.Fatal("real FFmpeg pipe did not retain active speech on stop")
	}
	last := received[len(received)-1]
	if last.Type != "activity" || last.Speech || last.AtMS != clips[len(clips)-1].EndSample*1000/16000 {
		t.Fatal("EOF state or final audio endpoint missing")
	}
	for _, clip := range clips {
		if _, err = readDeskTestWave(clip.Path); err != nil {
			t.Fatal(err)
		}
	}
}

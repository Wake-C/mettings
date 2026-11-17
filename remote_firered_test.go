package main

import (
	"bytes"
	"context"
	"fmt"
	"mettings.local/desktop/internal/meeting"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFireRedServiceChunksUseActualBoundaries(t *testing.T) {
	chunks, err := parseFireRedChunks([]byte("000000.wav,0.000000,30.080000\n000001.wav,30.080000,60.032000\n000002.wav,60.032000,61.250000\n"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 3 || chunks[1].StartMS != 30080 || chunks[2].EndMS != 61250 {
		t.Fatalf("wrong chunks: %+v", chunks)
	}
	var merged meeting.Result
	var first, second meeting.Result
	first.Result.Text = "first part"
	second.Result.Utterances = []meeting.Utterance{{Start: 50, End: 900, Text: "second part", Additions: meeting.Speaker{Speaker: "1"}}}
	appendFireRedChunk(&merged, first, chunks[0])
	appendFireRedChunk(&merged, second, chunks[1])
	appendFireRedChunk(&merged, meeting.Result{}, chunks[2])
	if len(merged.Result.Utterances) != 2 || merged.Result.Utterances[0].End != 30080 || merged.Result.Utterances[1].Start != 30130 || merged.Result.Utterances[1].End != 30980 || merged.Result.Utterances[1].Additions.Speaker != "" {
		t.Fatalf("bad merge: %+v", merged)
	}
}
func TestFireRedServiceRejectsInvalidChunks(t *testing.T) {
	for _, data := range []string{"", "a.wav,0,41\n", "a.wav,NaN,1\n", "a.wav,1,0\n", "a.wav,0\n"} {
		if _, err := parseFireRedChunks([]byte(data), t.TempDir()); err == nil {
			t.Fatalf("accepted invalid list %q", data)
		}
	}
}
func TestFireRedServiceIsNotABundledModel(t *testing.T) {
	if _, ok := findLocalModel("firered2-llm"); ok {
		t.Fatal("service listed as native model")
	}
	s := defaultSettings()
	s.ASR = meeting.Provider{Kind: "firered2-llm", Endpoint: "http://localhost:8000/v1", Model: "allendou/FireRedASR2-LLM-vllm"}
	selected, err := settingsForChoice(s, "configured")
	if err != nil || selected.ASR.Kind != "firered2-llm" || modelLabel(selected) != "FireRedASR2-LLM · 8B+ 服务" {
		t.Fatalf("bad service choice: %v", err)
	}
}

// Uses real FFmpeg with synthetic audio and a local API fixture; no remote
// deployment or private recording is required to check bounded uploads.
func TestFireRedServiceRealSegmentation(t *testing.T) {
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
	audio := filepath.Join(dir, "source.wav")
	if out, err := exec.Command(ffmpegPath(), "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono", "-t", "61.25", "-c:a", "pcm_s16le", audio).CombinedOutput(); err != nil {
		t.Fatalf("generate audio: %v %s", err, out)
	}
	original, err := os.ReadFile(audio)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("wrong path %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(2 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		if r.FormValue("model") != "allendou/FireRedASR2-LLM-vllm" || r.FormValue("response_format") != "json" || r.FormValue("prompt") != "" {
			t.Error("invalid FireRed request")
		}
		f, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		f.Close()
		if header.Size > 40*16000*2+1024 {
			t.Error("audio exceeds supported window")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 2 {
			fmt.Fprint(w, `{"text":""}`)
		} else {
			fmt.Fprintf(w, `{"text":"chunk %d"}`, calls)
		}
	}))
	defer server.Close()
	result, err := fireRedServiceTranscribe(context.Background(), meeting.Provider{Kind: "firered2-llm", Endpoint: server.URL + "/v1", Model: "allendou/FireRedASR2-LLM-vllm"}, meeting.Credentials{}, audio, dir)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(result.Result.Utterances) != 2 || result.Result.Utterances[1].Start < 60000 || result.Result.Utterances[1].End != 61250 {
		t.Fatalf("incorrect merged timeline: %d %+v", calls, result.Result)
	}
	after, err := os.ReadFile(audio)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("source changed")
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".firered-chunks-*"))
	if len(leftovers) != 0 {
		t.Fatal("temporary chunks not removed")
	}
}

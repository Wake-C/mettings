package meeting

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompatibleTranscription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing auth")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("model") != "test-asr" || r.FormValue("prompt") != "Mygo" {
			t.Error("missing model/hotwords")
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"segments":[{"start":1.25,"end":4.5,"speaker":"speaker_0","text":"Hello Mygo."}]}`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "audio.wav")
	os.WriteFile(path, []byte("audio"), 0600)
	result, err := Transcribe(context.Background(), Provider{Kind: "compatible", Endpoint: server.URL + "/v1", Model: "test-asr", ResponseFormat: "verbose_json"}, Credentials{Key: "test-key"}, path, []string{"Mygo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Result.Utterances) != 1 || result.Result.Utterances[0].Start != 1250 || result.Result.Utterances[0].Additions.Speaker != "speaker_0" {
		t.Fatalf("wrong normalized result: %+v", result)
	}
}
func TestTextOnlyDoesNotInventSpeaker(t *testing.T) {
	result, err := NormalizeCompatible([]byte(`{"text":"Hello."}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Result.Utterances) != 0 || result.Result.Text != "Hello." {
		t.Fatal("text-only result invented diarization")
	}
}
func TestRejectEmptyResult(t *testing.T) {
	if _, err := NormalizeCompatible([]byte(`{}`)); err == nil {
		t.Fatal("empty response accepted")
	}
}

func TestVolcDirectUploadPreservesRawResult(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Api-App-Key") != "test-app" || r.Header.Get("X-Api-Access-Key") != "test-access" {
			t.Error("Volc auth headers missing")
		}
		w.Header().Set("X-Api-Status-Code", "20000000")
		if strings.HasSuffix(r.URL.Path, "/submit") {
			var body struct {
				Audio struct {
					Data   string `json:"data"`
					Format string `json:"format"`
				} `json:"audio"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			audio, err := base64.StdEncoding.DecodeString(body.Audio.Data)
			if err != nil || string(audio) != "sample" || body.Audio.Format != "mp3" {
				t.Error("direct audio payload missing")
			}
			w.Write([]byte(`{}`))
			return
		}
		w.Write([]byte(`{"result":{"utterances":[{"start_time":10,"end_time":1000,"text":"你好","additions":{"speaker":"1","gender":"male"},"words":[{"text":"你好"}]}]}}`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "sample.mp3")
	os.WriteFile(path, []byte("sample"), 0600)
	result, err := Transcribe(context.Background(), Provider{Kind: "volc", Endpoint: server.URL}, Credentials{Key: "test-access", AppKey: "test-app"}, path, []string{"Mygo"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(result.Result.Utterances) != 1 || !strings.Contains(string(result.Raw), "gender") {
		t.Fatal("Volc result or raw payload lost")
	}
}

func TestExplicitEmptyTextIsNotAProviderFailure(t *testing.T) {
	if _, err := NormalizeCompatible([]byte(`{"text":""}`)); !errors.Is(err, ErrNoTranscript) {
		t.Fatalf("empty transcript not classified: %v", err)
	}
	if _, err := NormalizeCompatible([]byte(`{"text":"","error":{"message":"invalid audio"}}`)); err == nil || errors.Is(err, ErrNoTranscript) {
		t.Fatal("provider failure was hidden as no text")
	}
}

func TestOrdinaryMeetingMarkdownKeepsRelativeTime(t *testing.T) {
	var result Result
	result.Result.Utterances = []Utterance{{Start: 123000, End: 124000, Text: "sample"}}
	if !strings.Contains(Markdown(result), "**[00:02:03] 发言**") {
		t.Fatal("ordinary meeting timestamp changed")
	}
}

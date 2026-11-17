package main

import (
	"errors"
	"mettings.local/desktop/internal/meeting"
	"testing"
)

func TestNativeEmptyResultIsDifferentFromBrokenResponse(t *testing.T) {
	for _, body := range []string{`{}`, `{"result":{}}`, `{"result":{"utterances":null}}`, `not JSON`} {
		if _, err := parseLocalTranscript([]byte(body)); err == nil || errors.Is(err, meeting.ErrNoTranscript) {
			t.Fatalf("invalid response accepted as empty: %s", body)
		}
	}
	for _, body := range []string{`{"result":{"utterances":[]}}`, `{"result":{"utterances":[{"text":"  "}]}}`} {
		if _, err := parseLocalTranscript([]byte(body)); !errors.Is(err, meeting.ErrNoTranscript) {
			t.Fatalf("empty result became a failure: %v", err)
		}
	}
	result, err := parseLocalTranscript([]byte(`{"result":{"utterances":[{"text":"Hello","start_time":0,"end_time":200,"additions":{}}]},"diarization_fallback":true}`))
	if err != nil || !result.DiarizationFallback || len(result.Result.Utterances) != 1 {
		t.Fatal("valid fallback lost", err)
	}
}

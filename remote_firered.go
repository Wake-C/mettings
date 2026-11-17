package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"mettings.local/desktop/internal/meeting"
)

type fireRedChunk struct {
	Path           string
	StartMS, EndMS int64
}

// FireRedASR2-LLM supports at most 40 seconds per request. Segment to 30
// seconds and use FFmpeg's actual boundaries, including its final short chunk.
func fireRedServiceTranscribe(ctx context.Context, provider meeting.Provider, keys meeting.Credentials, audio, work string) (meeting.Result, error) {
	var result meeting.Result
	dir, err := os.MkdirTemp(work, ".firered-chunks-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)
	list := filepath.Join(dir, "chunks.csv")
	args := []string{"-hide_banner", "-loglevel", "error", "-i", audio, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-f", "segment", "-segment_time", "30", "-reset_timestamps", "1", "-segment_list_type", "csv", "-segment_list", list, filepath.Join(dir, "%06d.wav")}
	if err = exec.CommandContext(ctx, ffmpegPath(), args...).Run(); err != nil {
		return result, errors.New("无法为 FireRedASR2-LLM 准备音频分段")
	}
	data, err := os.ReadFile(list)
	if err != nil {
		return result, err
	}
	chunks, err := parseFireRedChunks(data, dir)
	if err != nil {
		return result, err
	}
	provider.Kind = "compatible"
	provider.ResponseFormat = "json"
	var raw []json.RawMessage
	for index, chunk := range chunks {
		part, err := meeting.Transcribe(ctx, provider, keys, chunk.Path, nil)
		if err != nil && !errors.Is(err, meeting.ErrNoTranscript) {
			return result, fmt.Errorf("FireRedASR2-LLM 第 %d/%d 段：%w", index+1, len(chunks), err)
		}
		appendFireRedChunk(&result, part, chunk)
		if len(part.Raw) > 0 {
			raw = append(raw, part.Raw)
		}
	}
	result.Raw, _ = json.Marshal(raw)
	if len(result.Result.Utterances) == 0 && strings.TrimSpace(result.Result.Text) == "" {
		return result, meeting.ErrNoTranscript
	}
	return result, nil
}

func parseFireRedChunks(data []byte, dir string) ([]fireRedChunk, error) {
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		return nil, errors.New("FireRed 音频分段清单无效")
	}
	chunks := make([]fireRedChunk, 0, len(rows))
	for _, row := range rows {
		if len(row) != 3 {
			return nil, errors.New("FireRed 音频分段清单无效")
		}
		start, e1 := strconv.ParseFloat(row[1], 64)
		end, e2 := strconv.ParseFloat(row[2], 64)
		if e1 != nil || e2 != nil || math.IsNaN(start) || math.IsNaN(end) || math.IsInf(start, 0) || math.IsInf(end, 0) || start < 0 || end <= start || end-start > 40 {
			return nil, errors.New("FireRed 音频分段超出 40 秒限制")
		}
		chunks = append(chunks, fireRedChunk{Path: filepath.Join(dir, filepath.Base(row[0])), StartMS: int64(math.Round(start * 1000)), EndMS: int64(math.Round(end * 1000))})
	}
	if len(chunks) == 0 {
		return nil, errors.New("音频没有可转写的分段")
	}
	return chunks, nil
}

func appendFireRedChunk(result *meeting.Result, part meeting.Result, chunk fireRedChunk) {
	if len(part.Result.Utterances) > 0 {
		for _, u := range part.Result.Utterances {
			u.Start += chunk.StartMS
			u.End += chunk.StartMS
			// A service's per-request speaker labels cannot identify the same person
			// across chunks. This route requests text only, so do not retain them.
			u.Additions.Speaker = ""
			result.Result.Utterances = append(result.Result.Utterances, u)
		}
	} else if text := strings.TrimSpace(part.Result.Text); text != "" {
		result.Result.Utterances = append(result.Result.Utterances, meeting.Utterance{Start: chunk.StartMS, End: chunk.EndMS, Text: text})
	}
}

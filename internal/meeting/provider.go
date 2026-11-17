package meeting

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Provider struct {
	Kind           string `json:"kind"`
	Endpoint       string `json:"endpoint"`
	Model          string `json:"model"`
	ResponseFormat string `json:"responseFormat"`
	ResourceID     string `json:"resourceId"`
}
type Credentials struct{ Key, AppKey string }
type Speaker struct {
	Speaker string `json:"speaker,omitempty"`
}
type Utterance struct {
	RecordedAtEstimated bool    `json:"recorded_at_estimated,omitempty"`
	RecordedAt          string  `json:"recorded_at,omitempty"`
	Start               int64   `json:"start_time"`
	End                 int64   `json:"end_time"`
	Text                string  `json:"text"`
	Additions           Speaker `json:"additions"`
}

var ErrNoTranscript = errors.New("未生成转写文本")

const EstimatedTimestampNotice = "历史时间按音频片段的保存时间估算。"

type Result struct {
	DiarizationFallback bool            `json:"diarization_fallback,omitempty"`
	Raw                 json.RawMessage `json:"-"`
	Result              struct {
		Text       string      `json:"text,omitempty"`
		Utterances []Utterance `json:"utterances,omitempty"`
	} `json:"result"`
}

func Endpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("Enter a provider base URL without credentials or query parameters")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return nil, errors.New("Use HTTPS, or HTTP for a local engine")
	}
	return u, nil
}
func Transcribe(ctx context.Context, p Provider, keys Credentials, audio string, hotwords []string) (Result, error) {
	if _, err := Endpoint(p.Endpoint); err != nil {
		return Result{}, err
	}
	switch p.Kind {
	case "compatible":
		return compatible(ctx, p, keys.Key, audio, hotwords)
	case "volc":
		return volc(ctx, p, keys, audio, hotwords)
	default:
		return Result{}, errors.New("Select a transcription provider in Settings")
	}
}
func compatible(ctx context.Context, p Provider, key, audio string, hotwords []string) (Result, error) {
	if strings.TrimSpace(p.Model) == "" {
		return Result{}, errors.New("Enter the ASR model in Settings")
	}
	file, err := os.Open(audio)
	if err != nil {
		return Result{}, err
	}
	defer file.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	form := multipart.NewWriter(writer)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(p.Endpoint, "/")+"/audio/transcriptions", reader)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	go func() {
		err := form.WriteField("model", p.Model)
		format := p.ResponseFormat
		if format == "" {
			format = "json"
		}
		if err == nil {
			err = form.WriteField("response_format", format)
		}
		if err == nil && len(hotwords) > 0 {
			err = form.WriteField("prompt", strings.Join(hotwords, ", "))
		}
		if err == nil && format == "diarized_json" {
			err = form.WriteField("chunking_strategy", "auto")
		}
		if err == nil {
			var part io.Writer
			part, err = form.CreateFormFile("file", filepath.Base(audio))
			if err == nil {
				_, err = io.Copy(part, file)
			}
		}
		if err == nil {
			err = form.Close()
		}
		writer.CloseWithError(err)
	}()
	response, err := (&http.Client{Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return Result{}, errors.New("ASR request failed; check the endpoint and connection")
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return Result{}, fmt.Errorf("ASR returned HTTP %d; check the model, key and audio size", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return Result{}, err
	}
	return NormalizeCompatible(body)
}
func NormalizeCompatible(body []byte) (Result, error) {
	var raw struct {
		Text     string `json:"text"`
		Segments []struct {
			Start   float64 `json:"start"`
			End     float64 `json:"end"`
			Text    string  `json:"text"`
			Speaker string  `json:"speaker"`
		} `json:"segments"`
	}
	var result Result
	result.Raw = append(json.RawMessage(nil), body...)
	if err := json.Unmarshal(body, &raw); err != nil {
		return result, errors.New("ASR did not return a JSON transcript")
	}
	result.Result.Text = strings.TrimSpace(raw.Text)
	for _, s := range raw.Segments {
		if strings.TrimSpace(s.Text) != "" {
			result.Result.Utterances = append(result.Result.Utterances, Utterance{Start: int64(s.Start * 1000), End: int64(s.End * 1000), Text: s.Text, Additions: Speaker{Speaker: s.Speaker}})
		}
	}
	if result.Result.Text == "" && len(result.Result.Utterances) == 0 {
		var envelope map[string]json.RawMessage
		_ = json.Unmarshal(body, &envelope)
		if problem, ok := envelope["error"]; ok && string(problem) != "null" && string(problem) != `""` {
			return result, errors.New("ASR service returned an error")
		}
		var explicitText *string
		if raw, ok := envelope["text"]; ok && json.Unmarshal(raw, &explicitText) == nil && explicitText != nil {
			return result, ErrNoTranscript
		}
		if string(envelope["segments"]) == "[]" {
			return result, ErrNoTranscript
		}
		return result, errors.New("ASR response contains no transcript fields")
	}
	return result, nil
}
func volc(ctx context.Context, p Provider, keys Credentials, audio string, hotwords []string) (Result, error) {
	var result Result
	if keys.Key == "" || keys.AppKey == "" {
		return result, errors.New("Add the Volcengine app key and access key in Settings")
	}
	b, err := os.ReadFile(audio)
	if err != nil {
		return result, err
	}
	request := map[string]any{"model_name": "bigmodel", "enable_itn": true, "enable_punc": true, "show_utterances": true, "enable_speaker_info": true, "enable_gender_detection": true, "vad_segment": false, "enable_ddc": false}
	if len(hotwords) > 0 {
		words := []map[string]string{}
		for _, word := range hotwords {
			words = append(words, map[string]string{"word": word})
		}
		corpus, _ := json.Marshal(map[string]any{"hotwords": words})
		request["corpus"] = map[string]string{"context": string(corpus)}
	}
	payload, _ := json.Marshal(map[string]any{"user": map[string]string{"uid": "mettings"}, "audio": map[string]string{"data": base64.StdEncoding.EncodeToString(b), "format": strings.TrimPrefix(filepath.Ext(audio), ".")}, "request": request})
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return result, err
	}
	id := hex.EncodeToString(idBytes)
	resource := p.ResourceID
	if resource == "" {
		resource = "volc.seedasr.auc"
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	send := func(action string, body []byte) ([]byte, string, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(p.Endpoint, "/")+"/api/v3/auc/bigmodel/"+action, bytes.NewReader(body))
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Api-App-Key", keys.AppKey)
		req.Header.Set("X-Api-Access-Key", keys.Key)
		req.Header.Set("X-Api-Resource-Id", resource)
		req.Header.Set("X-Api-Request-Id", id)
		if action == "submit" {
			req.Header.Set("X-Api-Sequence", "-1")
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, "", errors.New("Volcengine request failed; check the connection")
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return nil, "", fmt.Errorf("Volcengine returned HTTP %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		return data, resp.Header.Get("X-Api-Status-Code"), err
	}
	_, code, err := send("submit", payload)
	if err != nil {
		return result, err
	}
	if code != "20000000" {
		return result, fmt.Errorf("Volcengine rejected the request (%s)", code)
	}
	for i := 0; i < 180; i++ {
		data, code, err := send("query", []byte(`{}`))
		if err != nil {
			return result, err
		}
		if code == "20000000" {
			result.Raw = append(json.RawMessage(nil), data...)
			if err = json.Unmarshal(data, &result); err != nil {
				return result, err
			}
			if result.Result.Text == "" && len(result.Result.Utterances) == 0 {
				return result, errors.New("Volcengine returned no speech")
			}
			return result, nil
		}
		if code != "20000001" && code != "20000002" {
			return result, fmt.Errorf("Volcengine transcription failed (%s)", code)
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return result, errors.New("Volcengine is still processing after 15 minutes; retry later")
}
func Markdown(result Result) string {
	var b strings.Builder
	b.WriteString("# 转写稿\n\n")
	for _, u := range result.Result.Utterances {
		if u.RecordedAtEstimated {
			b.WriteString("> " + EstimatedTimestampNotice + "\n\n")
			break
		}
	}
	for _, u := range result.Result.Utterances {
		s := u.Start / 1000
		name := u.Additions.Speaker
		if name == "" {
			name = "发言"
		}
		stamp := fmt.Sprintf("%02d:%02d:%02d", s/3600, s%3600/60, s%60)
		if at, err := time.Parse(time.RFC3339Nano, u.RecordedAt); err == nil {
			stamp = at.Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(&b, "**[%s] %s**：%s\n\n", stamp, name, strings.TrimSpace(u.Text))
	}
	if len(result.Result.Utterances) == 0 {
		b.WriteString(result.Result.Text + "\n")
	}
	return b.String()
}

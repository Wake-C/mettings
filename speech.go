package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mettings.local/desktop/internal/meeting"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const voiceModelID = "campplus-zh-cn-f682b514"

func runtimeDir() string { return filepath.Join(dataDir(), "runtime") }
func ffmpegPath() string { return filepath.Join(runtimeDir(), "ffmpeg", "ffmpeg") }
func speechPath() string {
	exe, _ := os.Executable()
	p := filepath.Join(filepath.Dir(exe), "..", "Resources", "engine", "mettings-speech")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return filepath.Join("build", "Mettings.app", "Contents", "Resources", "engine", "mettings-speech")
}
func voiceModel() string   { return filepath.Join(runtimeDir(), "campplus.onnx") }
func voiceLibrary() string { return filepath.Join(dataDir(), "voiceprint", "native-campplus.json") }
func wav(ctx context.Context, source, target string, start, end int64) error {
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	args = append(args, "-i", source)
	if start > 0 || end > start {
		// ASR offsets use decoded PCM. Container timestamps can include gaps,
		// so input seeking can select a shorter or different voice sample.
		trim := fmt.Sprintf("aresample=16000,asetpts=N/SR/TB,atrim=start=%.3f", float64(start)/1000)
		if end > start {
			trim += fmt.Sprintf(":end=%.3f", float64(end)/1000)
		}
		args = append(args, "-af", trim+",asetpts=PTS-STARTPTS")
		if end > start {
			args = append(args, "-t", fmt.Sprintf("%.3f", float64(end-start)/1000))
		}
	}
	args = append(args, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", target)
	if exec.CommandContext(ctx, ffmpegPath(), args...).Run() != nil {
		return errors.New("音频处理失败，请在设置中检查本机组件")
	}
	return nil
}
func localTranscribe(ctx context.Context, s Settings, audio, dir string) (meeting.Result, error) {
	var result meeting.Result
	model, ok := findLocalModel(s.LocalModel)
	if !ok {
		return result, errors.New("请选择本地模型")
	}
	path := filepath.Join(dir, "local-input.wav")
	if err := wav(ctx, audio, path, 0, 0); err != nil {
		return result, err
	}
	defer os.Remove(path)
	asr := filepath.Join(runtimeDir(), localModelDirectory(s.LocalModel))
	hints := filepath.Join(dir, ".asr-hotwords.txt")
	if err := os.WriteFile(hints, []byte(s.Hotwords), 0600); err != nil {
		return result, err
	}
	defer os.Remove(hints)
	cmd := exec.CommandContext(ctx, speechPath(), "transcribe", model.Kind, asr, voiceModel(), filepath.Join(runtimeDir(), "sherpa-onnx-pyannote-segmentation-3-0", "model.onnx"), hints, path)
	b, err := cmd.Output()
	if err != nil {
		return result, errors.New("本地语音识别失败，请检查组件是否安装完整")
	}
	return parseLocalTranscript(b)
}
func parseLocalTranscript(data []byte) (meeting.Result, error) {
	var result meeting.Result
	var envelope struct {
		Result *struct {
			Text       *string              `json:"text"`
			Utterances *[]meeting.Utterance `json:"utterances"`
		} `json:"result"`
		Fallback bool `json:"diarization_fallback"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Result == nil || (envelope.Result.Text == nil && envelope.Result.Utterances == nil) {
		return result, errors.New("本地语音识别返回了无效结果")
	}
	result.DiarizationFallback = envelope.Fallback
	if envelope.Result.Text != nil {
		result.Result.Text = *envelope.Result.Text
	}
	if envelope.Result.Utterances != nil {
		for _, u := range *envelope.Result.Utterances {
			if strings.TrimSpace(u.Text) != "" {
				result.Result.Utterances = append(result.Result.Utterances, u)
			}
		}
	}
	if len(result.Result.Utterances) == 0 && strings.TrimSpace(result.Result.Text) == "" {
		return result, meeting.ErrNoTranscript
	}
	return result, nil
}
func embedVoice(ctx context.Context, path string) ([]float64, error) {
	b, err := exec.CommandContext(ctx, speechPath(), "embed", voiceModel(), path).Output()
	if err != nil {
		return nil, errors.New("声纹提取失败，请检查本机组件或换一段更清晰的声音")
	}
	var vector []float64
	if json.Unmarshal(b, &vector) != nil || len(vector) == 0 {
		return nil, errors.New("未获取到声纹向量")
	}
	return vector, nil
}

type UnknownSpeaker struct {
	Timeline string `json:"timeline,omitempty"`
	Speaker  string `json:"spk"`
	Name     string `json:"name,omitempty"`
	Reason   string `json:"reason"`
	Segment  string `json:"segment_path"`
	Start    int64  `json:"start_ms"`
	End      int64  `json:"end_ms"`
	Labeled  bool   `json:"labeled,omitempty"`
}
type VoiceLibrary struct {
	Model   string                 `json:"model"`
	Voices  map[string][]float64   `json:"voices"`
	Samples map[string][][]float64 `json:"samples,omitempty"`
}

func loadVoiceLibrary() (VoiceLibrary, error) {
	library := VoiceLibrary{Model: voiceModelID, Voices: map[string][]float64{}}
	b, err := os.ReadFile(voiceLibrary())
	if os.IsNotExist(err) {
		return library, nil
	}
	if err != nil {
		return library, err
	}
	if json.Unmarshal(b, &library) != nil || library.Model != voiceModelID {
		return library, errors.New("声纹库模型不匹配，请重新录入声音")
	}
	if library.Voices == nil {
		library.Voices = map[string][]float64{}
	}
	return library, nil
}
func attribute(ctx context.Context, s Settings, dir, audio string) (bool, error) {
	b, err := os.ReadFile(filepath.Join(dir, "transcript.json"))
	if err != nil {
		return false, err
	}
	var result meeting.Result
	if err = json.Unmarshal(b, &result); err != nil {
		return false, err
	}
	text := meeting.Correct(meeting.Markdown(result), s.Corrections)
	var aliases map[string]string
	_ = json.Unmarshal([]byte(s.Aliases), &aliases)
	library, libraryErr := loadVoiceLibrary()
	chosen := map[string]meeting.Utterance{}
	for _, u := range result.Result.Utterances {
		spk := u.Additions.Speaker
		if spk != "" && u.End-u.Start > chosen[spk].End-chosen[spk].Start {
			chosen[spk] = u
		}
	}
	existing := []UnknownSpeaker{}
	if b, e := os.ReadFile(filepath.Join(dir, "unidentified.json")); e == nil {
		_ = json.Unmarshal(b, &existing)
	}
	labels := map[string]bool{}
	for _, entry := range existing {
		labels[entry.Speaker] = entry.Labeled
	}
	names := map[string]string{}
	if b, e := os.ReadFile(filepath.Join(dir, "speaker_map.json")); e == nil {
		if err = json.Unmarshal(b, &names); err != nil {
			return false, err
		}
	}
	if names == nil {
		names = map[string]string{}
	}
	vectors := map[string][]float64{}
	if b, e := os.ReadFile(filepath.Join(dir, "speaker_embeddings.json")); e == nil {
		_ = json.Unmarshal(b, &vectors)
	}
	if vectors == nil {
		vectors = map[string][]float64{}
	}
	unknown := []UnknownSpeaker{}
	speakers := []string{}
	for spk := range chosen {
		speakers = append(speakers, spk)
	}
	sort.Strings(speakers)
	if len(speakers) > 0 {
		if err = os.MkdirAll(filepath.Join(dir, "segments"), 0700); err != nil {
			return false, err
		}
	}
	for i, spk := range speakers {
		if labels[spk] && names[spk] != "" {
			for _, entry := range existing {
				if entry.Speaker == spk {
					entry.Name = names[spk]
					unknown = append(unknown, entry)
					break
				}
			}
			continue
		}
		delete(names, spk)
		u := chosen[spk]
		end := u.End
		if end-u.Start > 30000 {
			end = u.Start + 30000
		}
		rel := fmt.Sprintf("segments/seg_%d.wav", i+1)
		clip := filepath.Join(dir, rel)
		entry := UnknownSpeaker{Timeline: "pcm-v1", Speaker: spk, Segment: rel, Start: u.Start, End: end, Labeled: labels[spk], Reason: "尚未录入声纹"}
		if err = wav(ctx, audio, clip, u.Start, end); err != nil {
			entry.Reason = "片段切割失败"
			unknown = append(unknown, entry)
			continue
		}
		if libraryErr != nil {
			entry.Reason = libraryErr.Error()
		} else {
			vector, e := embedVoice(ctx, clip)
			if e != nil {
				entry.Reason = e.Error()
			} else {
				vectors[spk] = vector
				if name, reason := matchVoiceWithReason(vector, library, s.VoiceThreshold); name != "" {
					names[spk] = name
					entry.Name = name
					entry.Reason = ""
				} else {
					entry.Reason = reason
				}
			}
		}
		unknown = append(unknown, entry)
	}
	applySpeakerMatches(unknown, names, vectors, library, s.VoiceThreshold)
	if err = meeting.WriteJSON(filepath.Join(dir, "speaker_embeddings.json"), vectors); err != nil {
		return false, err
	}
	text = meeting.MapSpeakers(meeting.MapSpeakers(text, names), aliases)
	if err = os.WriteFile(filepath.Join(dir, "transcript.md"), []byte(text), 0600); err != nil {
		return false, err
	}
	if err = meeting.WriteJSON(filepath.Join(dir, "speaker_map.json"), names); err != nil {
		return false, err
	}
	if err = meeting.WriteJSON(filepath.Join(dir, "unidentified.json"), unknown); err != nil {
		return false, err
	}
	for _, entry := range unknown {
		if !entry.Labeled && entry.Name == "" {
			return true, nil
		}
	}
	return false, nil
}
func labelVoice(ctx context.Context, s Settings, dir, spk, name string) error {
	return labelVoiceWithEmbed(ctx, s, dir, spk, name, embedVoice)
}

func labelVoiceWithEmbed(ctx context.Context, s Settings, dir, spk, name string, embed func(context.Context, string) ([]float64, error)) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "\n\r*[]\\") {
		return errors.New("请输入有效姓名")
	}
	var aliases map[string]string
	_ = json.Unmarshal([]byte(s.Aliases), &aliases)
	if canonical := aliases[name]; canonical != "" {
		name = canonical
	}
	var unknown []UnknownSpeaker
	b, err := os.ReadFile(filepath.Join(dir, "unidentified.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &unknown); err != nil {
		return err
	}
	index := -1
	for i, e := range unknown {
		if e.Speaker == spk {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("找不到待标注的说话人")
	}
	library, err := loadVoiceLibrary()
	if err != nil {
		return err
	}
	{
		clip := filepath.Join(dir, unknown[index].Segment)
		if filepath.Ext(clip) != ".wav" {
			converted := filepath.Join(dir, "label-input.wav")
			if err = wav(ctx, clip, converted, 0, 0); err != nil {
				return err
			}
			defer os.Remove(converted)
			clip = converted
		}
		vector, e := embed(ctx, clip)
		if e != nil {
			return e
		}
		oldName := unknown[index].Name
		if oldName == "" {
			var prior map[string]string
			if data, e := os.ReadFile(filepath.Join(dir, "speaker_map.json")); e == nil {
				_ = json.Unmarshal(data, &prior)
				oldName = prior[spk]
			}
		}
		if unknown[index].Labeled && oldName != "" && oldName != name {
			library.removeSample(oldName, vector)
		}
		library.addSample(name, vector)
		if err = os.MkdirAll(filepath.Dir(voiceLibrary()), 0700); err != nil {
			return err
		}
		if err = meeting.WriteJSON(voiceLibrary(), library); err != nil {
			return err
		}
	}
	unknown[index].Labeled = true
	unknown[index].Name = name
	unknown[index].Reason = ""
	mapping := map[string]string{}
	if data, e := os.ReadFile(filepath.Join(dir, "speaker_map.json")); e == nil {
		_ = json.Unmarshal(data, &mapping)
	}
	if mapping == nil {
		mapping = map[string]string{}
	}
	mapping[spk] = name
	if err = writeSpeakerTranscript(dir, s, mapping); err != nil {
		return err
	}
	if err = meeting.WriteJSON(filepath.Join(dir, "speaker_map.json"), mapping); err != nil {
		return err
	}
	return meeting.WriteJSON(filepath.Join(dir, "unidentified.json"), unknown)
}

func (library *VoiceLibrary) addSample(name string, vector []float64) {
	if _, ok := library.Voices[name]; !ok {
		library.Voices[name] = vector
		return
	}
	if slices.Equal(library.Voices[name], vector) {
		return
	}
	for _, sample := range library.Samples[name] {
		if slices.Equal(sample, vector) {
			return
		}
	}
	if library.Samples == nil {
		library.Samples = map[string][][]float64{}
	}
	library.Samples[name] = append(library.Samples[name], vector)
}

func (library *VoiceLibrary) removeSample(name string, vector []float64) {
	remaining := [][]float64{}
	if original, ok := library.Voices[name]; ok && !slices.Equal(original, vector) {
		remaining = append(remaining, original)
	}
	for _, sample := range library.Samples[name] {
		if !slices.Equal(sample, vector) {
			remaining = append(remaining, sample)
		}
	}
	delete(library.Voices, name)
	delete(library.Samples, name)
	if len(remaining) > 0 {
		library.Voices[name] = remaining[0]
	}
	if len(remaining) > 1 {
		library.Samples[name] = remaining[1:]
	}
}

// Render from immutable speaker IDs so correcting one Alice does not rename
// another speaker who happened to receive the same automatic name.
func writeSpeakerTranscript(dir string, s Settings, names map[string]string) error {
	b, err := os.ReadFile(filepath.Join(dir, "transcript.json"))
	var text string
	if err == nil {
		var result meeting.Result
		if err = json.Unmarshal(b, &result); err != nil {
			return err
		}
		if sessionDir := deskSessionForPath(s.Root, dir); sessionDir != "" {
			if timeline, e := readDeskTimeline(sessionDir); e == nil {
				stampDeskResult(&result, timeline, -1)
			}
		}
		text = meeting.Markdown(result)
	} else if os.IsNotExist(err) {
		b, err = os.ReadFile(filepath.Join(dir, "transcript.md"))
		if err != nil {
			return err
		}
		text = string(b)
	} else {
		return err
	}
	var aliases map[string]string
	_ = json.Unmarshal([]byte(s.Aliases), &aliases)
	text = meeting.MapSpeakers(meeting.MapSpeakers(meeting.Correct(text, s.Corrections), names), aliases)
	return os.WriteFile(filepath.Join(dir, "transcript.md"), []byte(text), 0600)
}

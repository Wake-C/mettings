package main

import "fmt"

const fireRedDirectory = "sherpa-onnx-fire-red-asr2-zh_en-int8-2026-02-26"
const qwenLargeDirectory = "qwen3-asr-1.7b-int8"
const qwenLargeRevision = "69eb686fd94a4a865bb5340a3d6ac0d7f1fec0d5"
const qwenLargeBase = "https://huggingface.co/thieunv-asilla/sherpa-onnx-qwen3-asr-1.7B-int8/resolve/" + qwenLargeRevision + "/"

var fireRedComponent = component{ID: "firered2", Name: "FireRedASR2-AED", URL: "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/" + fireRedDirectory + ".tar.bz2", SHA: "43015b3f1643a5688b4821e8ed323473d38b798c4ec291471fe00df1bcfc4f1c", Format: "tar.bz2", Bytes: 838589068, Files: map[string]string{
	fireRedDirectory + "/encoder.int8.onnx": fireRedDirectory + "/encoder.int8.onnx",
	fireRedDirectory + "/decoder.int8.onnx": fireRedDirectory + "/decoder.int8.onnx",
	fireRedDirectory + "/tokens.txt":        fireRedDirectory + "/tokens.txt",
}}

func qwenLargeParts() []component {
	files := []struct {
		ID, Path, SHA string
		Bytes         int64
	}{
		{"frontend", "conv_frontend.onnx", "3cb27a9fe94d95c938e476f2012b21aba2ec0bfceef33b0e58acd208946bafdd", 48080441},
		{"encoder", "encoder.int8.onnx", "a5deedae034ece715de8ed204378d8c77f889af3a60c2566581135e84cced7cd", 314222162},
		{"decoder", "decoder.int8.onnx", "c43c853fa6e97d08365cb8a5502b360b595cd43c00dc60e4d8ca7cc18cad460b", 2037458645},
		{"vocab", "tokenizer/vocab.json", "ca10d7e9fb3ed18575dd1e277a2579c16d108e32f27439684afa0e10b1440910", 2776833},
		{"merges", "tokenizer/merges.txt", "8831e4f1a044471340f7c0a83d7bd71306a5b867e95fd870f74d0c5308a904d5", 1671853},
		{"tokenizer", "tokenizer/tokenizer_config.json", "4942d005604266809309cabc9f4e9cb89ce855d59b14681fdc0e1cc62ea26c4c", 12487},
	}
	parts := make([]component, 0, len(files))
	for index, file := range files {
		parts = append(parts, component{ID: "qwen17-" + file.ID, Name: fmt.Sprintf("Qwen3-ASR 1.7B · %d/6", index+1), URL: qwenLargeBase + file.Path, SHA: file.SHA, Format: "file", Bytes: file.Bytes, Files: map[string]string{"model": qwenLargeDirectory + "/" + file.Path}})
	}
	return parts
}

type localModelDefinition struct {
	ID, Label, Tier, Description, Kind, Directory string
	Parts                                         []component
}

func localModels() []localModelDefinition {
	return []localModelDefinition{
		{"sensevoice", "SenseVoice", "轻量", "中 / 英 / 日 / 韩 / 粤语 · int8", "sensevoice", "sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2025-09-09", []component{components[1]}},
		{"qwen3", "Qwen3-ASR 0.6B", "标准", "多语言 · int8", "qwen3", qwenDirectory, []component{qwenComponent}},
		{"firered2", "FireRedASR2-AED", "标准", "中文 / 方言 / 英文 · int8", "firered2", fireRedDirectory, []component{fireRedComponent}},
		{"qwen3-1.7b", "Qwen3-ASR 1.7B", "大型", "多语言 · int8 · 较高内存占用", "qwen3-1.7b", qwenLargeDirectory, qwenLargeParts()},
	}
}
func findLocalModel(id string) (localModelDefinition, bool) {
	for _, model := range localModels() {
		if model.ID == id {
			return model, true
		}
	}
	return localModelDefinition{}, false
}
func localModelDirectory(id string) string { model, _ := findLocalModel(id); return model.Directory }
func componentsForModel(id string) []component {
	model, ok := findLocalModel(id)
	if !ok {
		return nil
	}
	result := []component{}
	for _, c := range components {
		if c.ID == "sensevoice" {
			result = append(result, model.Parts...)
		} else {
			result = append(result, c)
		}
	}
	return result
}

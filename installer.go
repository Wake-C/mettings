package main

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type component struct {
	ID, Name, URL, SHA, Format string
	Bytes                      int64
	Files                      map[string]string
	Executable                 bool
}

var components = []component{
	{ID: "ffmpeg", Name: "录音与音频处理", URL: "https://www.osxexperts.net/ffmpeg9arm.zip", SHA: "d0c06c5c68ce48af3143b262f7a9118a7c9f67de1e237fcc24ffb14df9c67af9", Format: "zip", Bytes: 23 << 20, Files: map[string]string{"ffmpeg": "ffmpeg/ffmpeg"}, Executable: true},
	{ID: "sensevoice", Name: "本地语音转写", URL: "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2025-09-09.tar.bz2", SHA: "7305f7905bfcf77fa0b39388a313f3da35c68d971661a65475b56fb2162c8e63", Format: "tar.bz2", Bytes: 165783878, Files: map[string]string{
		"sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2025-09-09/model.int8.onnx": "sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2025-09-09/model.int8.onnx",
		"sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2025-09-09/tokens.txt":      "sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2025-09-09/tokens.txt"}},
	{ID: "campplus", Name: "声纹识别", URL: "https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-recongition-models/3dspeaker_speech_campplus_sv_zh-cn_16k-common.onnx", SHA: "f682b514c05d947ee3fa91cd6ec6c5c7543479a128373fa29b1faedccd21fd11", Format: "file", Bytes: 28281138, Files: map[string]string{"model": "campplus.onnx"}},
	{ID: "segmentation", Name: "说话人分段", URL: "https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-segmentation-models/sherpa-onnx-pyannote-segmentation-3-0.tar.bz2", SHA: "24615ee884c897d9d2ba09bb4d30da6bb1b15e685065962db5b02e76e4996488", Format: "tar.bz2", Bytes: 6958444, Files: map[string]string{"sherpa-onnx-pyannote-segmentation-3-0/model.onnx": "sherpa-onnx-pyannote-segmentation-3-0/model.onnx", "sherpa-onnx-pyannote-segmentation-3-0/LICENSE": "licenses/pyannote-LICENSE"}},
}

const qwenDirectory = "sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25"

var qwenComponent = component{ID: "qwen3", Name: "Qwen3-ASR 0.6B · int8", URL: "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/" + qwenDirectory + ".tar.bz2", SHA: "393f8a14e2f5fb96746aaab342997a40641001fbd5bf9592a080a8329178ee96", Format: "tar.bz2", Bytes: 878702423, Files: map[string]string{
	qwenDirectory + "/conv_frontend.onnx":              qwenDirectory + "/conv_frontend.onnx",
	qwenDirectory + "/encoder.int8.onnx":               qwenDirectory + "/encoder.int8.onnx",
	qwenDirectory + "/decoder.int8.onnx":               qwenDirectory + "/decoder.int8.onnx",
	qwenDirectory + "/tokenizer/vocab.json":            qwenDirectory + "/tokenizer/vocab.json",
	qwenDirectory + "/tokenizer/merges.txt":            qwenDirectory + "/tokenizer/merges.txt",
	qwenDirectory + "/tokenizer/tokenizer_config.json": qwenDirectory + "/tokenizer/tokenizer_config.json",
}}

type SetupState struct {
	CaptureReady bool   `json:"captureReady"`
	DeskReady    bool   `json:"deskReady"`
	Model        string `json:"model"`
	InstallBytes int64  `json:"installBytes"`
	Ready        bool   `json:"ready"`
	Installing   bool   `json:"installing"`
	Name         string `json:"name"`
	Downloaded   int64  `json:"downloaded"`
	Total        int64  `json:"total"`
	Error        string `json:"error"`
}

func installed(root string, c component) bool {
	b, err := os.ReadFile(filepath.Join(root, "."+c.ID+".installed"))
	if err != nil || strings.TrimSpace(string(b)) != c.SHA {
		return false
	}
	for _, rel := range c.Files {
		if info, e := os.Stat(filepath.Join(root, rel)); e != nil || info.Size() == 0 {
			return false
		}
	}
	return true
}

var vadComponent = component{ID: "silero-vad", Name: "本地语音检测", URL: "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad.onnx", SHA: "9e2449e1087496d8d4caba907f23e0bd3f78d91fa552479bb9c23ac09cbb1fd6", Format: "file", Bytes: 643854, Files: map[string]string{"model": "silero_vad.onnx"}}

func captureReady() bool { return installed(runtimeDir(), components[0]) }
func deskReady() bool    { return captureReady() && installed(runtimeDir(), vadComponent) }
func (d *Desktop) InstallRecordingTools() error {
	return d.installComponents([]component{components[0]})
}
func (d *Desktop) InstallDeskTools() error {
	return d.installComponents([]component{components[0], vadComponent})
}

func runtimeReady(model string) bool {
	if _, ok := findLocalModel(model); !ok {
		return false
	}
	for _, c := range componentsForModel(model) {
		if !installed(runtimeDir(), c) {
			return false
		}
	}
	return true
}
func (d *Desktop) SetupStatus() SetupState {
	d.mu.Lock()
	state := d.setup
	state.Model = d.settings.LocalModel
	d.mu.Unlock()
	state.Ready = runtimeReady(state.Model)
	state.CaptureReady = captureReady()
	state.DeskReady = deskReady()
	for _, c := range componentsForModel(state.Model) {
		if !installed(runtimeDir(), c) {
			state.InstallBytes += c.Bytes
		}
	}
	return state
}
func (d *Desktop) InstallRuntime() error {
	model := d.GetSettings().LocalModel
	if _, ok := findLocalModel(model); !ok {
		return errors.New("请选择本地模型")
	}
	return d.installComponents(componentsForModel(model))
}
func (d *Desktop) installComponents(plan []component) error {
	d.mu.Lock()
	if d.busy || d.record != nil || d.desk != nil {
		d.mu.Unlock()
		return errors.New("请先停止当前会话")
	}
	d.busy = true
	d.setup = SetupState{Installing: true}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.mu.Unlock()
	go func() {
		defer cancel()
		var err error
		for _, c := range plan {
			if installed(runtimeDir(), c) {
				continue
			}
			d.mu.Lock()
			d.setup.Name = c.Name
			d.setup.Downloaded = 0
			d.setup.Total = c.Bytes
			d.mu.Unlock()
			err = installComponent(ctx, runtimeDir(), c, func(done, total int64) {
				d.mu.Lock()
				d.setup.Downloaded = done
				if total > 0 {
					d.setup.Total = total
				}
				d.mu.Unlock()
			})
			if err != nil {
				break
			}
		}
		d.mu.Lock()
		d.busy = false
		d.cancel = nil
		d.setup.Installing = false
		if err != nil {
			d.setup.Error = err.Error()
		} else {
			d.setup.Ready = true
			d.phase = "本机组件已就绪"
		}
		d.mu.Unlock()
	}()
	return nil
}
func (d *Desktop) CancelSetup() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.setup.Installing && d.cancel != nil {
		d.cancel()
	}
}

type progressWriter struct {
	writer      io.Writer
	done, total int64
	progress    func(int64, int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.writer.Write(b)
	p.done += int64(n)
	p.progress(p.done, p.total)
	return n, err
}
func installComponent(ctx context.Context, root string, c component, progress func(int64, int64)) error {
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(root, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	req, err := http.NewRequestWithContext(ctx, "GET", c.URL, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return errors.New("下载未完成，请检查网络后重试")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("组件下载失败 (HTTP %d)，请重试", response.StatusCode)
	}
	archive := filepath.Join(temp, "download")
	out, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(&progressWriter{writer: io.MultiWriter(out, hash), total: response.ContentLength, progress: progress}, response.Body)
	closeErr := out.Close()
	if err != nil {
		return errors.New("下载中断，可重新安装")
	}
	if closeErr != nil {
		return closeErr
	}
	if hex.EncodeToString(hash.Sum(nil)) != c.SHA {
		return errors.New("下载校验失败，请重新下载组件")
	}
	stage := filepath.Join(temp, "files")
	found := map[string]bool{}
	copyEntry := func(name string, reader io.Reader) error {
		rel, ok := c.Files[name]
		if !ok {
			return nil
		}
		target := filepath.Join(stage, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if c.Executable {
			mode = 0755
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(file, reader)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		found[name] = true
		return closeErr
	}
	switch c.Format {
	case "file":
		file, e := os.Open(archive)
		if e != nil {
			return e
		}
		err = copyEntry("model", file)
		file.Close()
	case "zip":
		zr, e := zip.OpenReader(archive)
		if e != nil {
			return e
		}
		defer zr.Close()
		for _, entry := range zr.File {
			if _, ok := c.Files[entry.Name]; !ok {
				continue
			}
			r, e := entry.Open()
			if e != nil {
				return e
			}
			err = copyEntry(entry.Name, r)
			r.Close()
			if err != nil {
				break
			}
		}
	case "tar.bz2":
		file, e := os.Open(archive)
		if e != nil {
			return e
		}
		defer file.Close()
		tr := tar.NewReader(bzip2.NewReader(file))
		for {
			header, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			if header.Typeflag == tar.TypeReg {
				if err = copyEntry(strings.TrimPrefix(header.Name, "./"), tr); err != nil {
					break
				}
			}
		}
	default:
		return errors.New("Unknown component archive format")
	}
	if err != nil {
		return err
	}
	if len(found) != len(c.Files) {
		return errors.New("组件包缺少文件，请重新安装")
	}
	if err = ctx.Err(); err != nil {
		return errors.New("安装已取消")
	}
	for _, rel := range c.Files {
		target := filepath.Join(root, rel)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if err = os.Rename(filepath.Join(stage, rel), target); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(root, "."+c.ID+".installed"), []byte(c.SHA), 0600)
}

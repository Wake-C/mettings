package meeting

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DownloadAudio preserves the remote source locally before transcription starts.
func DownloadAudio(ctx context.Context, raw, dir string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil {
		return "", errors.New("请输入不含登录凭据的音频 URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return "", errors.New("请使用 HTTPS 音频链接")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return "", err
	}
	response, err := (&http.Client{Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return "", errors.New("无法下载音频，请检查链接和网络")
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return "", errors.New("音频链接不可用，请使用可直接下载的链接")
	}
	ext := strings.ToLower(filepath.Ext(u.Path))
	switch ext {
	case ".wav", ".mp3", ".m4a", ".mp4", ".aac", ".flac", ".ogg", ".webm":
	default:
		ext = ".bin"
	}
	name := "recording" + ext
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path+".part", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer os.Remove(path + ".part")
	n, err := io.Copy(file, response.Body)
	closeErr := file.Close()
	if err != nil {
		return "", errors.New("音频下载中断，请重试")
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n == 0 {
		return "", errors.New("音频链接返回了空文件")
	}
	if err = os.Rename(path+".part", path); err != nil {
		return "", err
	}
	return name, nil
}

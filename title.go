package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mettings.local/desktop/internal/meeting"
	"net/http"
	"strings"
	"time"
)

// Accept a service origin, API base, or the full Chat Completions URL.
func titleURL(endpoint string) (string, error) {
	u, err := meeting.Endpoint(endpoint)
	if err != nil {
		return "", err
	}
	if u.Path == "" {
		u.Path = "/v1"
	}
	if !strings.HasSuffix(u.Path, "/chat/completions") {
		u.Path += "/chat/completions"
	}
	return u.String(), nil
}

func (d *Desktop) TestTitleService(endpoint, model, key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if strings.TrimSpace(key) == "" {
		key = readKey(endpoint, "title")
	}
	_, err := generateTitle(ctx, Settings{TitleEndpoint: endpoint, TitleModel: model}, "这是一条连接测试，请返回标题：连接测试。", strings.TrimSpace(key))
	return err
}

func generateTitle(ctx context.Context, s Settings, transcript, key string) (string, error) {
	endpoint, err := titleURL(s.TitleEndpoint)
	if err != nil {
		return "", err
	}
	s.TitleModel = strings.TrimSpace(s.TitleModel)
	if s.TitleModel == "" {
		return "", errors.New("请填写标题模型")
	}
	rs := []rune(transcript)
	if len(rs) > 8000 {
		transcript = string(rs[:8000])
	}
	body := map[string]any{"model": s.TitleModel, "messages": []map[string]string{{"role": "system", "content": "为会议转写稿生成不超过20字的中文标题。只返回标题，不加引号。"}, {"role": "user", "content": transcript}}, "stream": false}
	if strings.HasPrefix(strings.ToLower(s.TitleModel), "deepseek") {
		body["thinking"] = map[string]string{"type": "disabled"}
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errors.New("连接超时，请检查服务地址与网络")
		}
		return "", errors.New("无法连接标题服务，请检查服务地址与网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", titleHTTPError(resp.StatusCode)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil || len(result.Choices) == 0 {
		return "", errors.New("服务未返回有效的 Chat Completions 响应，请检查 API 地址和模型")
	}
	title := meeting.SafeTitle(strings.Trim(result.Choices[0].Message.Content, "\"“”\n "))
	if title == "" {
		return "", errors.New("模型未返回标题文本，请检查模型是否支持 Chat Completions")
	}
	return title, nil
}

func titleHTTPError(status int) error {
	advice := "请检查服务配置或稍后重试"
	switch status {
	case 401, 403:
		advice = "请检查密钥与模型访问权限"
	case 404:
		advice = "请检查 API 地址与模型名称"
	case 400, 422:
		advice = "请检查模型名称及 Chat Completions 兼容性"
	case 429:
		advice = "请求受限，请检查配额或稍后重试"
	}
	return fmt.Errorf("标题服务 HTTP %d：%s", status, advice)
}

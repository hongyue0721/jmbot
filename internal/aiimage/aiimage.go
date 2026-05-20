package aiimage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

type Config struct {
	BaseURL    string
	APIKey     string
	Model      string
	Size       string
	Timeout    time.Duration
	MaxRetries int
}

type Result struct {
	ImageURL string
	B64JSON  string
}

func GenerateImage(cfg Config, prompt string) (*Result, error) {
	var result *Result
	var lastErr error
	for i := 0; i < cfg.MaxRetries; i++ {
		result, lastErr = generateImageOnce(cfg, prompt)
		if lastErr == nil {
			return result, nil
		}
		log.Printf("AI generate attempt %d/%d failed: %v", i+1, cfg.MaxRetries, lastErr)
	}
	return nil, fmt.Errorf("AI 生图失败（已重试%d次）：%s", cfg.MaxRetries-1, briefError(lastErr))
}

func generateImageOnce(cfg Config, prompt string) (*Result, error) {
	url := strings.TrimRight(cfg.BaseURL, "/") + "/images/generations"

	body := map[string]any{
		"model":           cfg.Model,
		"prompt":          prompt,
		"n":               1,
		"size":            cfg.Size,
		"response_format": "url",
	}

	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	client := &http.Client{Timeout: cfg.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API status %d", resp.StatusCode)
	}

	var apiResp struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response failed")
	}
	if len(apiResp.Data) == 0 {
		return nil, fmt.Errorf("API returned empty data")
	}
	return &Result{ImageURL: apiResp.Data[0].URL, B64JSON: apiResp.Data[0].B64JSON}, nil
}

func EditImage(cfg Config, prompt string, imageBytes []byte) (*Result, error) {
	var result *Result
	var lastErr error
	for i := 0; i < cfg.MaxRetries; i++ {
		result, lastErr = editImageOnce(cfg, prompt, imageBytes)
		if lastErr == nil {
			return result, nil
		}
		log.Printf("AI edit attempt %d/%d failed: %v", i+1, cfg.MaxRetries, lastErr)
	}
	return nil, fmt.Errorf("AI 图生图失败（已重试%d次）：%s", cfg.MaxRetries-1, briefError(lastErr))
}

func editImageOnce(cfg Config, prompt string, imageBytes []byte) (*Result, error) {
	url := strings.TrimRight(cfg.BaseURL, "/") + "/images/edits"

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	_ = w.WriteField("model", cfg.Model)
	_ = w.WriteField("prompt", prompt)
	_ = w.WriteField("size", cfg.Size)
	_ = w.WriteField("n", "1")
	_ = w.WriteField("response_format", "url")

	fw, err := w.CreateFormFile("image", "image.png")
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(imageBytes); err != nil {
		return nil, err
	}
	_ = w.Close()

	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	client := &http.Client{Timeout: cfg.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API status %d", resp.StatusCode)
	}

	var apiResp struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response failed")
	}
	if len(apiResp.Data) == 0 {
		return nil, fmt.Errorf("API returned empty data")
	}
	return &Result{ImageURL: apiResp.Data[0].URL, B64JSON: apiResp.Data[0].B64JSON}, nil
}

func briefError(err error) string {
	if err == nil {
		return "未知错误"
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "未知错误"
	}
	if idx := strings.Index(msg, "sk-"); idx >= 0 {
		end := idx + 20
		if end > len(msg) {
			end = len(msg)
		}
		msg = msg[:idx] + "sk-***" + msg[end:]
	}
	if len([]rune(msg)) > 200 {
		msg = string([]rune(msg)[:200]) + "..."
	}
	return msg
}

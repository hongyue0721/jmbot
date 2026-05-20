package app

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"napcat-jm-go/internal/aiimage"
)

func (a *App) handleAIImageCommand(rawMessage string, data map[string]any, messageType string, groupID, userID int64) bool {
	// image on / off / help
	if m := mustMatch(`^(?:/)?image\s+(on|off|help)$`, rawMessage); m != nil {
		switch m[1] {
		case "on":
			if !a.requireAdmin(messageType, groupID, userID, "仅管理员可开启 AI 画图") {
				return true
			}
			a.cfgMu.Lock()
			a.cfg.AIImageEnabled = true
			a.cfgMu.Unlock()
			a.saveConfig()
			a.sendMessage(messageType, groupID, userID, "AI 画图功能已开启，使用 image2 <提示词> 生成图片")
			return true
		case "off":
			if !a.requireAdmin(messageType, groupID, userID, "仅管理员可关闭 AI 画图") {
				return true
			}
			a.cfgMu.Lock()
			a.cfg.AIImageEnabled = false
			a.cfgMu.Unlock()
			a.saveConfig()
			a.sendMessage(messageType, groupID, userID, "AI 画图功能已关闭")
			return true
		case "help":
			a.sendMessage(messageType, groupID, userID, "AI 画图使用说明：\n1) image on - 开启画图功能\n2) image off - 关闭画图功能\n3) image2 <提示词> - 生成图片\n4) 引用图片后 image2 <提示词> - 图生图")
			return true
		}
	}

	m := mustMatch(`^(?:/)?image2\s+(.+)$`, rawMessage)
	if m == nil {
		return false
	}
	prompt := strings.TrimSpace(m[1])
	if prompt == "" {
		return false
	}

	cfg := a.currentConfig()
	if !cfg.AIImageEnabled {
		a.sendMessage(messageType, groupID, userID, "AI 画图功能未开启，请发送 image on 开启")
		return true
	}
	if cfg.AIImageAPIKey == "" {
		a.sendMessage(messageType, groupID, userID, "AI 画图未配置 API Key（请联系管理员设置 ai_image_api_key）")
		return true
	}

	aiCfg := aiimage.Config{
		BaseURL:    cfg.AIImageBaseURL,
		APIKey:     cfg.AIImageAPIKey,
		Model:      cfg.AIImageModel,
		Size:       cfg.AIImageSize,
		Timeout:    time.Duration(cfg.AIImageTimeout) * time.Second,
		MaxRetries: cfg.AIImageMaxRetries,
	}

	a.sendMessage(messageType, groupID, userID, "正在生成图片...")

	imageBytes, _ := a.extractAIImageBytes(data)

	var result *aiimage.Result
	var err error

	if len(imageBytes) > 0 {
		result, err = aiimage.EditImage(aiCfg, prompt, imageBytes)
	} else {
		result, err = aiimage.GenerateImage(aiCfg, prompt)
	}

	if err != nil {
		if messageType == "group" && groupID > 0 {
			a.bot.SendGroupMsgWithAtText(groupID, userID, err.Error())
		} else {
			a.bot.SendPrivateMessage(userID, err.Error())
		}
		return true
	}

	var imageRef string
	if result.B64JSON != "" {
		imageRef = "base64://" + strings.TrimPrefix(result.B64JSON, "data:image/")
		if idx := strings.Index(imageRef, ","); idx > 0 {
			imageRef = "base64://" + imageRef[idx+1:]
		}
	} else if strings.HasPrefix(result.ImageURL, "data:") {
		if idx := strings.Index(result.ImageURL, ","); idx > 0 {
			imageRef = "base64://" + result.ImageURL[idx+1:]
		}
	} else {
		imageRef = result.ImageURL
	}
	if imageRef == "" {
		msg := "AI 画图返回为空"
		if messageType == "group" && groupID > 0 {
			a.bot.SendGroupMsgWithAtText(groupID, userID, msg)
		} else {
			a.bot.SendPrivateMessage(userID, msg)
		}
		return true
	}

	if messageType == "group" && groupID > 0 {
		a.bot.SendGroupMsgWithAtAndImage(groupID, userID, imageRef)
	} else {
		a.bot.SendPrivateMsgWithImage(userID, imageRef)
	}
	return true
}

func (a *App) extractAIImageBytes(data map[string]any) ([]byte, error) {
	replyID := extractReplyMessageID(data)
	if replyID > 0 {
		msgData, err := a.bot.GetMsg(replyID)
		if err == nil && msgData != nil {
			sources := extractSoutuImageSourcesFromEvent(msgData)
			for _, src := range sources {
				if src.ImageURL != "" {
					return downloadImageBytes(src.ImageURL)
				}
				if len(src.ImageBytes) > 0 {
					return src.ImageBytes, nil
				}
			}
		}
	}

	sources := extractSoutuImageSourcesFromEvent(data)
	for _, src := range sources {
		if src.ImageURL != "" {
			return downloadImageBytes(src.ImageURL)
		}
		if len(src.ImageBytes) > 0 {
			return src.ImageBytes, nil
		}
	}

	refs := extractSoutuImageFileRefsFromEvent(data)
	for _, ref := range refs {
		u, err := a.bot.GetImageURL(ref)
		if err == nil && u != "" {
			return downloadImageBytes(u)
		}
	}

	return nil, nil
}

func extractReplyMessageID(data map[string]any) int64 {
	msg, ok := data["message"].([]any)
	if !ok {
		return 0
	}
	for _, seg := range msg {
		m, ok := seg.(map[string]any)
		if !ok || toString(m["type"]) != "reply" {
			continue
		}
		dm := mapGet(m, "data")
		return toInt64(dm["id"])
	}
	return 0
}

func downloadImageBytes(imageURL string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download image status %d", resp.StatusCode)
	}
	return 	io.ReadAll(resp.Body)
}

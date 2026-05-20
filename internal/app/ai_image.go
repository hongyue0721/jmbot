package app

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"napcat-jm-go/internal/aiimage"
)

const defaultWaitingImage = "base64://iVBORw0KGgoAAAANSUhEUgAAAHgAAAB4CAYAAAA5ZDbSAAADzUlEQVR4nO2bQW7UQBBF7VFmx5qTwAUabsCGu7DOXdhwA8gF4CSssxukIEsYWZY909Pd1fXLeU+KFCmJ5cyb/7td9gwDAAAAAAAAAAAAAAAAAAAAAAAAAGQxDoH5/eH9y/z92x8/m/4vl+8P/499/vgn7Os0RhdrIXctOLLoMbLYnnKjih6jirWSu1XPe8IjiD4N4vSWu2aSuyfyVtIVGKOJ7SH3shI3C74mVDXNYySxHnLX8qKtz2MUsb1q+ZKx3uZUs4po+TW495qbg4q8MIK9a7lmXc79fS9cXziVWq5ZX9XrWiLBysmNXtdugiPU8prSgYdnXY+vvZZb1q1iXctVtLLciMOO0xFrOaclrFCra5kEt5A7iZ2+So51bpg+pSSP0WfLFjf9L43WUoU6fxicKZVi+TTHOePmQu5xvAceY6RbfpZSLZ/w8LyfPEaQ2+MRHYtx5bW/LTlGGME5grxv9PeSbC34pCZ33gkPB+RcOMsOs8nak6s83Wq9Aeu98TJ70XLWzdykqshdM4sqqdmaqpdL8FLQvfWrKrc2jb2SbLIGb13OHHltvVRIrj2Ge4KPKrUV1kmWmUVHq+eWa6flpZLrJOsIgms3W9a4nFCky6IWn4DwxP2EVCdWR0qxFJE3ZReRR2UBAAAAAAAAAAAAAF41775+CjuwT4/Pcuc+qgr+9fmb3LmVyH368sb1/5B7ZCea2Bzx89fggOSLGS3FqVBej3SfFNfRKGJbptsq4WYVHXmzdCRM1+BayRHeJKminsNW9LJiSyUdtaafFmKXbw4r2V120TVJVE5xykzvLHUpsdeu2jQlazklqVTdUacMQXupXP+tZVV3/XzwJKtUlIrodEPsLVm9r4dNK3pLhnLl1pCzadqSa73Rcplk3SN5/SbxfIOkjWrN3Q17TbLMBe9VarSNV1rteO9Jnuccust6dk1I7pq6dwzrNTn9k1Mq41pyDyO4heSc1LaSnRpdn3rLnei6I+0hOfdYFmndOpb3LUSZ24Ut19WSY6XH55dW40OlG/9dBd9KlucO+alRqmqvkw+b4JYbMq9hSBJKrpvg6cVXTrJlcj0e33FLcKlk71FlhFqWruhoSU6CtbxkjCDynnFlz4Qn4eRKJfjeulao6RRArozgaHWdxGt5icS7rLSut36312xaPbkzUidz7zrbu7qTyPjxEBV9TZb37cIocidkTyz3mnj5M6sEJ9HPHeUgf4I1l1Mt8HjMpiVhTnRJr81V6vj0oxXhTniJZT2nDg+lAwAAAAAAAAAAAAAAAAAAAAAAAMCgw19G18MTjD40swAAAABJRU5ErkJggg=="

func (a *App) handleAIImageCommand(rawMessage string, data map[string]any, messageType string, groupID, userID int64) bool {
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

	m := mustMatch(`(?:^|])(?:\s*)image2\s+(.+)$`, rawMessage)
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

	useImageToImage := false
	imageBytes, extractErr := a.extractAIImageBytes(data)
	if extractErr != nil {
		log.Printf("[AI Image] extract image failed: %v", extractErr)
	}
	if len(imageBytes) > 0 {
		useImageToImage = true
	}

	waitingImageSent := false
	if messageType == "group" && groupID > 0 {
		if cfg.AIImageWaitingImage != "" {
			waitingImageSent = a.bot.SendGroupImage(groupID, cfg.AIImageWaitingImage)
		}
	}

	var result *aiimage.Result
	var err error

	if useImageToImage {
		result, err = aiimage.EditImage(aiCfg, prompt, imageBytes)
		if strings.Contains(err.Error(), "不支持图生图") {
			if !waitingImageSent && messageType == "group" && groupID > 0 {
				a.bot.SendGroupMsgWithAtText(groupID, userID, "当前模型不支持图生图，正在尝试文生图...")
			}
			result, err = aiimage.GenerateImage(aiCfg, prompt)
		}
	} else {
		if !waitingImageSent && !(messageType == "group" && groupID > 0) {
			a.sendMessage(messageType, groupID, userID, "正在生成图片...")
		}
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
	// 尝试从回复消息中提取图片
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
			refs := extractSoutuImageFileRefsFromEvent(msgData)
			for _, ref := range refs {
				u, getErr := a.bot.GetImageURL(ref)
				if getErr == nil && u != "" {
					return downloadImageBytes(u)
				}
			}
			// 尝试从被回复消息的 image 字段直接提取
			if img, ok := extractImageFromMessage(msgData); ok {
				return downloadImageBytes(img)
			}
		}
	}

	// 尝试从当前消息中提取图片（image2 与图片在同一消息的情况）
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
	return io.ReadAll(resp.Body)
}

// extractImageFromMessage 从 NapCat 消息的 records 或 message 字段提取图片 URL
func extractImageFromMessage(data map[string]any) (string, bool) {
	// 从 records 数组中提取（被回复消息的图片在这里）
	if records, ok := data["records"].([]any); ok {
		for _, rec := range records {
			if rm, ok := rec.(map[string]any); ok {
				if url := findImageInElements(rm); url != "" {
					return url, true
				}
			}
		}
	}
	// 从 elements 中提取
	if url := findImageInElements(data); url != "" {
		return url, true
	}
	return "", false
}

func findImageInElements(data map[string]any) string {
	elems, ok := data["elements"].([]any)
	if !ok {
		return ""
	}
	for _, e := range elems {
		m, ok := e.(map[string]any)
		if !ok || toString(m["type"]) != "image" {
			continue
		}
		d := mapGet(m, "data")
		if u := toString(d["url"]); u != "" {
			return u
		}
	}
	return ""
}

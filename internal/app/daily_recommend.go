package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// startDailyRecommend 启动每日推荐定时任务
func (a *App) startDailyRecommend() {
	cfg := a.currentConfig()
	if !cfg.DailyRecommendEnabled {
		log.Printf("[Daily] 每日推荐未启用")
		return
	}

	log.Printf("[Daily] 每日推荐已启用，发送时间: %02d:%02d, 群数: %d", 
		cfg.DailyRecommendHour, cfg.DailyRecommendMinute, len(cfg.DailyRecommendGroups))

	go func() {
		for {
			now := time.Now()
			next := time.Date(now.Year(), now.Month(), now.Day(), 
				cfg.DailyRecommendHour, cfg.DailyRecommendMinute, 0, 0, now.Location())
			if now.After(next) {
				next = next.Add(24 * time.Hour)
			}
			duration := next.Sub(now)
			log.Printf("[Daily] 下次发送时间: %v (等待 %v)", next.Format("2006-01-02 15:04:05"), duration)
			time.Sleep(duration)
			a.sendDailyRecommend()
		}
	}()
}

// sendDailyRecommend 发送每日推荐
func (a *App) sendDailyRecommend() {
	cfg := a.currentConfig()
	if !cfg.DailyRecommendEnabled || len(cfg.DailyRecommendGroups) == 0 {
		return
	}

	log.Printf("[Daily] 开始获取每日推荐")

	// 获取JM周榜
	jmAlbums := a.getJMTrendingAlbums()
	log.Printf("[Daily] JM周榜获取: %d 本", len(jmAlbums))

	// 获取哔咔日榜
	bikaAlbums := a.getBikaTrendingAlbums()
	log.Printf("[Daily] 哔咔日榜获取: %d 本", len(bikaAlbums))

	// 合并并随机选择
	allAlbums := append(jmAlbums, bikaAlbums...)
	if len(allAlbums) == 0 {
		log.Printf("[Daily] 没有可用的本子")
		return
	}

	// 随机选择一个
	selected := allAlbums[rand.Intn(len(allAlbums))]
	log.Printf("[Daily] 选择推荐: %s - %s", selected.ID, selected.Title)

	// 发送到开启的群
	for _, groupID := range cfg.DailyRecommendGroups {
		a.sendDailyRecommendToGroup(groupID, selected, cfg)
	}
}

func (a *App) sendDailyRecommendToGroup(groupID int64, album Album, cfg Config) {
	// 构建信息
	isBika := strings.HasPrefix(album.ID, "bika_")
	source := "JM"
	downloadID := album.ID
	if isBika {
		source = "Bika"
		downloadID = strings.TrimPrefix(album.ID, "bika_")
	}

	tags := strings.Join(album.Tags, ", ")
	if len(tags) > 100 {
		tags = tags[:100] + "..."
	}

	infoMsg := fmt.Sprintf("【每日本子推荐】\n来源：%s\n标题：%s\n标签：%s\n章节数：%d\n\n回复本聊天记录+序号下载", 
		source, album.Title, tags, album.Episodes)

	// 获取封面
	coverPath := ""
	if isBika {
		// 哔咔：获取封面图片URL并下载
		coverPath = a.downloadBikaCover(downloadID)
	} else {
		// JM：获取本地manga第一页
		if mangaPath, ok, _ := a.findMangaPageByID(album.ID, 1); ok && fileExists(mangaPath) {
			coverPath = mangaPath
		}
	}

	// 使用转发消息发送
	senderID := cfg.CardUserID
	nickname := cfg.CardNickname
	if senderID <= 0 {
		senderID = 10000
	}
	if nickname == "" {
		nickname = "每日推荐"
	}

	nodes := make([]map[string]any, 0, 2)

	// 信息节点
	nodes = append(nodes, map[string]any{
		"type": "node",
		"data": map[string]any{
			"user_id":  senderID,
			"nickname": nickname,
			"content": []map[string]any{
				{"type": "text", "data": map[string]any{"text": infoMsg}},
			},
		},
	})

	// 封面节点
	if coverPath != "" && fileExists(coverPath) {
		if pf, err := a.bot.prepareForwardFile(cfg, coverPath); err == nil && len(pf.candidates) > 0 {
			nodes = append(nodes, map[string]any{
				"type": "node",
				"data": map[string]any{
					"user_id":  senderID,
					"nickname": nickname,
					"content": []map[string]any{
						{"type": "image", "data": map[string]any{"file": pf.candidates[0]}},
					},
				},
			})
		}
	}

	// 发送
	params := map[string]any{
		"group_id": groupID,
		"message":  nodes,
	}
	a.bot.send("send_group_forward_msg", params, echo("daily_recommend", groupID), 60*time.Second)

	// 清理封面临时文件
	if coverPath != "" && strings.Contains(coverPath, "/tmp/") {
		_ = os.Remove(coverPath)
	}

	// 缓存推荐结果供回复下载
	if isBika {
		bikaSearchCacheMu.Lock()
		bikaSearchCache[fmt.Sprintf("daily:%d", groupID)] = BikaPendingSearch{
			Results: []BikaSearchResult{{
				ID:    downloadID,
				Title: album.Title,
				Tags:  album.Tags,
			}},
			At: time.Now(),
		}
		bikaSearchCacheMu.Unlock()
	} else {
		a.searchMu.Lock()
		a.search[fmt.Sprintf("daily:%d", groupID)] = PendingSearch{
			AlbumID: downloadID,
			Title:   album.Title,
			At:      time.Now(),
		}
		a.searchMu.Unlock()
	}
}

// downloadBikaCover 下载哔咔封面到临时文件
func (a *App) downloadBikaCover(comicID string) string {
	token := a.getBikaUserToken(0)
	if token == "" {
		return ""
	}

	// 获取章节图片第一页
	pages, _, err := a.bika.GetChapterImages(comicID, 1, 1, token)
	if err != nil || len(pages) == 0 {
		return ""
	}

	first := pages[0]
	imageURL := buildBikaImageURL(first.Media.FileServer, first.Media.Path)
	tmpPath := fmt.Sprintf("/tmp/bika_cover_%s%s", comicID, filepath.Ext(first.Media.OriginalName))
	if err := downloadBikaFile(imageURL, tmpPath, token, "original"); err != nil {
		return ""
	}
	return tmpPath
}

// getJMTrendingAlbums 获取JM周榜
func (a *App) getJMTrendingAlbums() []Album {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 使用搜索热门关键词获取周榜
	keywords := []string{"本周", "热门", "推荐"}
	var albums []Album

	for _, keyword := range keywords {
		data, err := a.jm.reqAPI(ctx, "/search", map[string]string{
			"main_tag":     "0",
			"search_query": keyword,
			"page":         "1",
			"o":            "mv", // 按浏览量排序
			"t":            "a",
		})
		if err != nil {
			continue
		}

		content, ok := data["content"].([]any)
		if !ok {
			continue
		}

		for _, item := range content {
			row, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id := toJMID(anyToString(row["id"]))
			title := anyToString(row["name"])
			tags := parseTags(row["tags"])
			episodes := toInt64(row["episodes"])
			if id != "" && title != "" {
				albums = append(albums, Album{
					ID:       id,
					Title:    title,
					Tags:     tags,
					Episodes: int(episodes),
				})
			}
		}
		if len(albums) >= 5 {
			break
		}
	}

	return albums
}

// parseTags 解析标签
func parseTags(v any) []string {
	switch t := v.(type) {
	case []any:
		var tags []string
		for _, item := range t {
			if s, ok := item.(string); ok {
				tags = append(tags, s)
			}
		}
		return tags
	case []string:
		return t
	case string:
		if t != "" {
			return strings.Split(t, ",")
		}
	}
	return nil
}

// getBikaTrendingAlbums 获取哔咔日榜
func (a *App) getBikaTrendingAlbums() []Album {
	if a.bika == nil {
		return nil
	}

	token := a.getBikaUserToken(0) // 使用全局token
	if token == "" {
		return nil
	}

	// 获取哔咔排行榜
	endpoint := "comics/leaderboard?tt=1&ct=VC" // tt=1 为日榜
	respBody, err := a.bika.makeRequest("GET", endpoint, nil, token)
	if err != nil {
		log.Printf("[Daily] 哔咔排行榜获取失败: %v", err)
		return nil
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Comics struct {
				Docs []struct {
					ID          string   `json:"_id"`
					Title       string   `json:"title"`
					Author      string   `json:"author"`
					Tags        []string `json:"tags"`
					Categories  []string `json:"categories"`
					EPSCount    int      `json:"epsCount"`
					Description string   `json:"description"`
				} `json:"docs"`
			} `json:"comics"`
		} `json:"data"`
	}

	if err := json.Unmarshal(respBody, &resp); err != nil {
		log.Printf("[Daily] 哔咔排行榜解析失败: %v", err)
		return nil
	}

	var albums []Album
	for _, comic := range resp.Data.Comics.Docs {
		albums = append(albums, Album{
			ID:       "bika_" + comic.ID,
			Title:    comic.Title,
			Tags:     append(comic.Tags, comic.Categories...),
			Episodes: comic.EPSCount,
		})
	}

	return albums
}

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"
)

// DailyRecommendConfig 每日推荐配置
type DailyRecommendConfig struct {
	Enabled bool  `yaml:"enabled"`
	Hour    int   `yaml:"hour"`
	Minute  int   `yaml:"minute"`
	Groups  []int64 `yaml:"groups"`
}

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

	// 获取哔咔周榜
	bikaAlbums := a.getBikaTrendingAlbums()
	log.Printf("[Daily] 哔咔周榜获取: %d 本", len(bikaAlbums))

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
		msg := fmt.Sprintf("【每日本子推荐】\n\n%s\n\n标签: %s\n章节数: %d\n\n发送 /jm %s 下载", 
			selected.Title, strings.Join(selected.Tags, ", "), selected.Episodes, selected.ID)
		a.sendMessage("group", groupID, 0, msg)
	}
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
			if id != "" && title != "" {
				albums = append(albums, Album{
					ID:    id,
					Title: title,
					Tags:  []string{},
				})
			}
		}
	}

	return albums
}

// getBikaTrendingAlbums 获取哔咔周榜
func (a *App) getBikaTrendingAlbums() []Album {
	if a.bika == nil {
		return nil
	}

	token := a.getBikaUserToken(0) // 使用全局token
	if token == "" {
		return nil
	}

	// 获取哔咔排行榜
	endpoint := "comics/leaderboard?tt=7&ct=VC"
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

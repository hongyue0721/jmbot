package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	bikaAPIKey    = "C69BAF41DA5ABD1FFEDC6D2FEA56B"
	bikaSecretKey = "~d}$Q7$eIni=V)9\\RK/P.RM4;9[7|@/CA}b~OW!3?EV`:<>M7pddUBL5n|0/*Cn"
	bikaNonce     = "4ce7a7aa759b40f794d189a88b84aba8"
	bikaPlatform  = "android"
	bikaVersion   = "2.2.1.3.3.4"
	bikaChannel   = "1"
	bikaUUID      = "defaultUuid"
	bikaBuildVer  = "45"
)

type BikaClient struct {
	baseURL string
	token   string
	quality string
	client  *http.Client
}

type BikaConfig struct {
	Enabled bool   `yaml:"enabled"`
	BaseURL string `yaml:"base_url"`
	Token   string `yaml:"token"`
	Quality string `yaml:"quality"`
	Proxy   string `yaml:"proxy"`
}

type BikaAPIResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type BikaComic struct {
	ID          string   `json:"_id"`
	Title       string   `json:"title"`
	Author      string   `json:"author"`
	Artist      string   `json:"artist"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`
	Categories  []string `json:"categories"`
	EPSCount    int      `json:"epsCount"`
	Finished    bool     `json:"finished"`
	TotalViews  int      `json:"totalViews"`
	LikesCount  int      `json:"likesCount"`
	UpdatedAt   string   `json:"updatedAt"`
	CreatedAt   string   `json:"createdAt"`
	Thumb       struct {
		Path string `json:"path"`
	} `json:"thumb"`
}

type BikaSearchResult struct {
	ID          string   `json:"_id"`
	Title       string   `json:"title"`
	Author      string   `json:"author"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Categories  []string `json:"categories"`
	LikesCount  int      `json:"likesCount"`
	Finished    bool     `json:"finished"`
	Thumb       struct {
		Path string `json:"path"`
	} `json:"thumb"`
}

type BikaChapter struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Order int    `json:"order"`
}

type BikaPageItem struct {
	ID    string `json:"id"`
	Media struct {
		OriginalName string `json:"originalName"`
		Path         string `json:"path"`
		FileServer   string `json:"fileServer"`
	} `json:"media"`
}

func NewBikaClient(cfg BikaConfig) *BikaClient {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.go2778.com/"
	}
	quality := cfg.Quality
	if quality == "" {
		quality = "original"
	}

	client := &http.Client{Timeout: 60 * time.Second}
	if cfg.Proxy != "" {
		proxyURL, err := parseURL(cfg.Proxy)
		if err == nil {
			client.Transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		}
	}

	return &BikaClient{
		baseURL: baseURL,
		token:   cfg.Token,
		quality: quality,
		client:  client,
	}
}

func parseURL(rawURL string) (*url.URL, error) {
	return url.Parse(rawURL)
}

func (b *BikaClient) generateSignature(endpoint string, timestamp int64) string {
	sigStr := strings.ToLower(endpoint + fmt.Sprintf("%d", timestamp) + bikaNonce + "GET" + bikaAPIKey)
	h := hmac.New(sha256.New, []byte(bikaSecretKey))
	h.Write([]byte(sigStr))
	return hex.EncodeToString(h.Sum(nil))
}

func (b *BikaClient) generateSignaturePOST(endpoint string, timestamp int64) string {
	sigStr := strings.ToLower(endpoint + fmt.Sprintf("%d", timestamp) + bikaNonce + "POST" + bikaAPIKey)
	h := hmac.New(sha256.New, []byte(bikaSecretKey))
	h.Write([]byte(sigStr))
	return hex.EncodeToString(h.Sum(nil))
}

func (b *BikaClient) makeRequest(method, endpoint string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	fullURL := b.baseURL + endpoint
	timestamp := time.Now().Unix()

	var signature string
	if method == "POST" {
		signature = b.generateSignaturePOST(endpoint, timestamp)
	} else {
		signature = b.generateSignature(endpoint, timestamp)
	}

	req, err := http.NewRequest(method, fullURL, reqBody)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("api-key", bikaAPIKey)
	req.Header.Set("app-build-version", bikaBuildVer)
	req.Header.Set("app-platform", bikaPlatform)
	req.Header.Set("app-uuid", bikaUUID)
	req.Header.Set("app-version", bikaVersion)
	req.Header.Set("app-channel", bikaChannel)
	req.Header.Set("nonce", bikaNonce)
	req.Header.Set("time", fmt.Sprintf("%d", timestamp))
	req.Header.Set("signature", signature)
	req.Header.Set("accept", "application/vnd.picacomic.com.v1+json")
	req.Header.Set("User-Agent", "okhttp/3.8.1")

	if b.token != "" {
		req.Header.Set("authorization", b.token)
	}

	if strings.Contains(endpoint, "/pages") && b.quality != "" {
		req.Header.Set("image-quality", b.quality)
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return respBody, nil
}

func (b *BikaClient) Search(keyword string, page int) ([]BikaSearchResult, int, error) {
	payload := map[string]interface{}{
		"keyword": keyword,
	}
	endpoint := fmt.Sprintf("comics/advanced-search?page=%d", page)

	respBody, err := b.makeRequest("POST", endpoint, payload)
	if err != nil {
		return nil, 0, err
	}

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Comics struct {
				Docs  []BikaSearchResult `json:"docs"`
				Total int                `json:"total"`
				Pages int                `json:"pages"`
			} `json:"comics"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, 0, err
	}
	if resp.Code != 200 {
		return nil, 0, fmt.Errorf("bika api error: %s", resp.Message)
	}

	return resp.Data.Comics.Docs, resp.Data.Comics.Total, nil
}

func (b *BikaClient) GetComicDetail(comicID string) (*BikaComic, error) {
	endpoint := fmt.Sprintf("comics/%s", comicID)
	respBody, err := b.makeRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Comic BikaComic `json:"comic"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 200 {
		return nil, fmt.Errorf("bika api error: %s", resp.Message)
	}

	return &resp.Data.Comic, nil
}

func (b *BikaClient) GetChapters(comicID string, page int) ([]BikaChapter, int, error) {
	endpoint := fmt.Sprintf("comics/%s/eps?page=%d", comicID, page)
	respBody, err := b.makeRequest("GET", endpoint, nil)
	if err != nil {
		return nil, 0, err
	}

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			EPS struct {
				Docs  []BikaChapter `json:"docs"`
				Total int           `json:"total"`
				Pages int           `json:"pages"`
			} `json:"eps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, 0, err
	}
	if resp.Code != 200 {
		return nil, 0, fmt.Errorf("bika api error: %s", resp.Message)
	}

	return resp.Data.EPS.Docs, resp.Data.EPS.Total, nil
}

func (b *BikaClient) GetChapterImages(comicID string, order, page int) ([]BikaPageItem, int, error) {
	endpoint := fmt.Sprintf("comics/%s/order/%d/pages?page=%d", comicID, order, page)
	respBody, err := b.makeRequest("GET", endpoint, nil)
	if err != nil {
		return nil, 0, err
	}

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Pages struct {
				Docs  []BikaPageItem `json:"docs"`
				Total int            `json:"total"`
				Page  int            `json:"page"`
				Pages int            `json:"pages"`
			} `json:"pages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, 0, err
	}
	if resp.Code != 200 {
		return nil, 0, fmt.Errorf("bika api error: %s", resp.Message)
	}

	return resp.Data.Pages.Docs, resp.Data.Pages.Total, nil
}

func (b *BikaClient) DownloadChapter(ctx context.Context, comicID, comicTitle, epTitle string, epOrder int, outputDir string) (string, error) {
	comicTitle = sanitizeBikaFilename(strings.TrimSpace(comicTitle))
	epTitle = sanitizeBikaFilename(strings.TrimSpace(epTitle))
	epDir := filepath.Join(outputDir, fmt.Sprintf("bika_%s", comicID), fmt.Sprintf("%d_%s", epOrder, epTitle))

	if err := os.MkdirAll(epDir, 0755); err != nil {
		return "", fmt.Errorf("create dir failed: %v", err)
	}

	page := 1
	totalPages := 0
	var allPages []BikaPageItem

	for {
		pages, total, err := b.GetChapterImages(comicID, epOrder, page)
		if err != nil {
			return "", fmt.Errorf("get chapter images failed: %v", err)
		}

		if totalPages == 0 {
			totalPages = total
		}

		allPages = append(allPages, pages...)

		if page >= totalPages || len(pages) == 0 {
			break
		}
		page++
	}

	if len(allPages) == 0 {
		return "", fmt.Errorf("no images found")
	}

	var successCount int32
	var failCount int32
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)

	for i, item := range allPages {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, p BikaPageItem) {
			defer wg.Done()
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				return
			default:
			}

			imageURL := buildBikaImageURL(p.Media.FileServer, p.Media.Path)
			filename := filepath.Join(epDir, fmt.Sprintf("%03d_%s", idx+1, p.Media.OriginalName))

			if err := downloadBikaFile(imageURL, filename, b.token); err != nil {
				atomic.AddInt32(&failCount, 1)
				log.Printf("bika download image failed [%s]: %v", filename, err)
			} else {
				atomic.AddInt32(&successCount, 1)
			}
		}(i, item)
	}

	wg.Wait()

	if successCount == 0 {
		os.RemoveAll(epDir)
		return "", fmt.Errorf("all images download failed")
	}

	cbzPath := epDir + ".cbz"
	if err := zipDirToCBZ(epDir, cbzPath); err != nil {
		log.Printf("bika cbz pack failed: %v", err)
		return epDir, nil
	}
	os.RemoveAll(epDir)
	return cbzPath, nil
}

func buildBikaImageURL(fileServer, path string) string {
	if strings.Contains(fileServer, "go2778") || strings.Contains(fileServer, "static") {
		return fileServer + "/static/" + path
	}
	directURL := fileServer + "/static/" + path
	return strings.ReplaceAll(directURL, "picacomic", "go2778")
}

func downloadBikaFile(url, filepath, token string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("api-key", bikaAPIKey)
	req.Header.Set("app-build-version", bikaBuildVer)
	req.Header.Set("app-platform", bikaPlatform)
	req.Header.Set("app-uuid", bikaUUID)
	req.Header.Set("app-version", bikaVersion)
	req.Header.Set("app-channel", bikaChannel)
	req.Header.Set("nonce", bikaNonce)
	req.Header.Set("accept", "application/vnd.picacomic.com.v1+json")

	if token != "" {
		req.Header.Set("authorization", token)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func sanitizeBikaFilename(name string) string {
	if len(name) > 50 {
		name = name[:50]
	}
	invalidChars := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", "[", "]"}
	for _, c := range invalidChars {
		name = strings.ReplaceAll(name, c, "_")
	}
	return name
}

func extractBikaIDFromInput(input string) string {
	input = strings.TrimSpace(input)
	re := regexp.MustCompile(`^[a-f0-9]{24}$`)
	if re.MatchString(input) {
		return input
	}
	return ""
}

type BikaPendingSearch struct {
	Results []BikaSearchResult
	At      time.Time
}

var bikaSearchCache = make(map[string]BikaPendingSearch)
var bikaSearchCacheMu sync.Mutex

func getBikaConfig(cfg *Config) BikaConfig {
	return BikaConfig{
		Enabled: cfg.BikaEnabled,
		BaseURL: cfg.BikaBaseURL,
		Token:   cfg.BikaToken,
		Quality: cfg.BikaQuality,
		Proxy:   cfg.BikaProxy,
	}
}

func (a *App) handleBikaCommand(rawMessage, messageType string, groupID, userID int64, scope string, data map[string]any) bool {
	if a.bika == nil {
		return false
	}

	// /bika help
	if matched(`^/bika\s+help$`, rawMessage) {
		msg := "Bika 漫画源命令：\n" +
			"1) /bika search <关键词>：搜索漫画\n" +
			"2) /bika look <ID>：查看漫画详情\n" +
			"3) /bika dl <ID> [章节]：下载漫画（可选指定章节）\n" +
			"4) /bika confirm <序号>：确认搜索结果下载"
		a.sendMessage(messageType, groupID, userID, msg)
		return true
	}

	// /bika search <keyword>
	if m := mustMatch(`^/bika\s+search\s+(.+)$`, rawMessage); m != nil {
		keyword := strings.TrimSpace(m[1])
		if keyword == "" {
			a.sendMessage(messageType, groupID, userID, "请输入搜索关键词")
			return true
		}
		a.sendMessage(messageType, groupID, userID, "正在Bika搜索："+keyword+" ...")

		results, total, err := a.bika.Search(keyword, 1)
		if err != nil {
			a.sendMessage(messageType, groupID, userID, "Bika搜索失败："+err.Error())
			return true
		}
		if len(results) == 0 {
			a.sendMessage(messageType, groupID, userID, "未找到相关漫画")
			return true
		}

		bikaSearchCacheMu.Lock()
		bikaSearchCache[scope] = BikaPendingSearch{Results: results, At: time.Now()}
		bikaSearchCacheMu.Unlock()

		var lines []string
		for i, r := range results {
			if i >= 10 {
				break
			}
			tags := strings.Join(r.Tags, ", ")
			if len(tags) > 50 {
				tags = tags[:50] + "..."
			}
			lines = append(lines, fmt.Sprintf("%d. [%s] %s\n   作者：%s 标签：%s", i+1, r.ID, r.Title, r.Author, tags))
		}
		msg := fmt.Sprintf("Bika搜索结果（共%d条）：\n%s\n\n回复 /bika confirm <序号> 下载", total, strings.Join(lines, "\n"))
		a.sendRecordMessage(messageType, groupID, userID, msg)
		return true
	}

	// /bika confirm <index>
	if m := mustMatch(`^/bika\s+confirm\s+(\d+)$`, rawMessage); m != nil {
		idx, _ := strconv.Atoi(m[1])
		if idx <= 0 {
			a.sendMessage(messageType, groupID, userID, "序号无效")
			return true
		}

		bikaSearchCacheMu.Lock()
		pending, ok := bikaSearchCache[scope]
		if ok && time.Since(pending.At) > 10*time.Minute {
			delete(bikaSearchCache, scope)
			ok = false
		}
		bikaSearchCacheMu.Unlock()

		if !ok || len(pending.Results) == 0 {
			a.sendMessage(messageType, groupID, userID, "没有待确认的搜索结果，请先搜索")
			return true
		}
		if idx > len(pending.Results) {
			a.sendMessage(messageType, groupID, userID, fmt.Sprintf("序号超出范围，最大为 %d", len(pending.Results)))
			return true
		}

		comic := pending.Results[idx-1]
		bikaSearchCacheMu.Lock()
		delete(bikaSearchCache, scope)
		bikaSearchCacheMu.Unlock()

		a.sendMessage(messageType, groupID, userID, fmt.Sprintf("开始下载Bika漫画：%s (ID: %s)", comic.Title, comic.ID))
		go a.bikaDownloadAndSend(comic.ID, "", messageType, groupID, userID)
		return true
	}

	// /bika look <id>
	if m := mustMatch(`^/bika\s+look\s+([a-f0-9]+)$`, rawMessage); m != nil {
		comicID := m[1]
		a.sendMessage(messageType, groupID, userID, "正在查询Bika漫画："+comicID)

		comic, err := a.bika.GetComicDetail(comicID)
		if err != nil {
			a.sendMessage(messageType, groupID, userID, "查询失败："+err.Error())
			return true
		}

		tags := strings.Join(comic.Tags, ", ")
		categories := strings.Join(comic.Categories, ", ")
		status := "连载中"
		if comic.Finished {
			status = "已完结"
		}
		msg := fmt.Sprintf("ID：%s\n标题：%s\n作者：%s\n画师：%s\n标签：%s\n分类：%s\n章节：%d\n状态：%s\n浏览：%d\n点赞：%d\n简介：%s",
			comic.ID, comic.Title, comic.Author, comic.Artist, tags, categories, comic.EPSCount, status, comic.TotalViews, comic.LikesCount, comic.Description)
		a.sendRecordMessage(messageType, groupID, userID, msg)
		return true
	}

	// /bika dl <id> [chapter]
	if m := mustMatch(`^/bika\s+dl\s+([a-f0-9]+)(?:\s+(\d+))?$`, rawMessage); m != nil {
		comicID := m[1]
		chapter := ""
		if m[2] != "" {
			chapter = m[2]
		}
		a.sendMessage(messageType, groupID, userID, fmt.Sprintf("开始下载Bika漫画 ID: %s", comicID))
		go a.bikaDownloadAndSend(comicID, chapter, messageType, groupID, userID)
		return true
	}

	return false
}

func (a *App) bikaDownloadAndSend(comicID, chapterStr string, messageType string, groupID, userID int64) {
	comic, err := a.bika.GetComicDetail(comicID)
	if err != nil {
		a.sendMessage(messageType, groupID, userID, "获取漫画信息失败："+err.Error())
		return
	}

	chapters, _, err := a.bika.GetChapters(comicID, 1)
	if err != nil {
		a.sendMessage(messageType, groupID, userID, "获取章节列表失败："+err.Error())
		return
	}

	if len(chapters) == 0 {
		a.sendMessage(messageType, groupID, userID, "该漫画没有章节")
		return
	}

	cfg := a.currentConfig()
	outputDir := cfg.CBZDir
	if outputDir == "" {
		outputDir = "./cbz/"
	}

	// 下载指定章节或全部章节
	var toDownload []BikaChapter
	if chapterStr != "" {
		chapterNum, err := strconv.Atoi(chapterStr)
		if err != nil {
			a.sendMessage(messageType, groupID, userID, "章节号无效")
			return
		}
		found := false
		for _, ch := range chapters {
			if ch.Order == chapterNum {
				toDownload = append(toDownload, ch)
				found = true
				break
			}
		}
		if !found {
			a.sendMessage(messageType, groupID, userID, fmt.Sprintf("未找到第%d章", chapterNum))
			return
		}
	} else {
		if len(chapters) > cfg.MaxEpisodes {
			a.sendMessage(messageType, groupID, userID, fmt.Sprintf("章节数过多(%d>%d)，请指定章节号", len(chapters), cfg.MaxEpisodes))
			return
		}
		toDownload = chapters
	}

	for _, ch := range toDownload {
		a.sendMessage(messageType, groupID, userID, fmt.Sprintf("正在下载：%s 第%d话 %s", comic.Title, ch.Order, ch.Title))

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.DownloadTimeout)*time.Second)
		result, err := a.bika.DownloadChapter(ctx, comicID, comic.Title, ch.Title, ch.Order, outputDir)
		cancel()

		if err != nil {
			a.sendMessage(messageType, groupID, userID, fmt.Sprintf("下载失败：第%d话 %s - %v", ch.Order, ch.Title, err))
			continue
		}

		// 发送文件
		ok := false
		if messageType == "group" {
			ok = a.bot.SendGroupFile(cfg, groupID, result)
		} else {
			ok = a.bot.SendPrivateFile(cfg, userID, result)
		}

		if !ok {
			failMsg := fmt.Sprintf("文件发送失败：%s 第%d话", comic.Title, ch.Order)
			a.sendMessage(messageType, groupID, userID, failMsg)
		}
	}

	a.sendMessage(messageType, groupID, userID, fmt.Sprintf("Bika漫画下载完成：%s", comic.Title))
}

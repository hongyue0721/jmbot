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
	"sort"
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
	proxy   string
}

type BikaConfig struct {
	Enabled bool   `yaml:"enabled"`
	BaseURL string `yaml:"base_url"`
	Token   string `yaml:"token"`
	Quality string `yaml:"quality"`
	Proxy   string `yaml:"proxy"`
}

type BikaUserToken struct {
	Token   string    `json:"token"`
	Name    string    `json:"name"`
	Email   string    `json:"email"`
	Expires time.Time `json:"expires"`
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

type BikaPendingSearch struct {
	Results []BikaSearchResult
	At      time.Time
}

var (
	bikaUserTokens   = make(map[int64]*BikaUserToken) // userID -> token
	bikaUserTokensMu sync.RWMutex
	bikaSearchCache  = make(map[string]BikaPendingSearch)
	bikaSearchCacheMu sync.Mutex
)

func NewBikaClient(cfg BikaConfig) *BikaClient {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.go2778.com/"
	}
	quality := cfg.Quality
	if quality == "" {
		quality = "original"
	}

	return &BikaClient{
		baseURL: baseURL,
		token:   cfg.Token,
		quality: quality,
		proxy:   cfg.Proxy,
	}
}

func (b *BikaClient) getHTTPClient() *http.Client {
	client := &http.Client{Timeout: 60 * time.Second}
	if b.proxy != "" {
		proxyURL, err := url.Parse(b.proxy)
		if err == nil {
			client.Transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		}
	}
	return client
}

func (b *BikaClient) generateSignature(endpoint, method string, timestamp int64) string {
	sigStr := strings.ToLower(endpoint + fmt.Sprintf("%d", timestamp) + bikaNonce + method + bikaAPIKey)
	h := hmac.New(sha256.New, []byte(bikaSecretKey))
	h.Write([]byte(sigStr))
	return hex.EncodeToString(h.Sum(nil))
}

func (b *BikaClient) makeRequest(method, endpoint string, body interface{}, token string) ([]byte, error) {
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
	signature := b.generateSignature(endpoint, method, timestamp)

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

	if token != "" {
		req.Header.Set("authorization", token)
	} else if b.token != "" {
		req.Header.Set("authorization", b.token)
	}

	if strings.Contains(endpoint, "/pages") && b.quality != "" {
		req.Header.Set("image-quality", b.quality)
	}

	resp, err := b.getHTTPClient().Do(req)
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

func (b *BikaClient) SignIn(email, password string) (*BikaUserToken, error) {
	payload := map[string]string{
		"email":    email,
		"password": password,
	}

	respBody, err := b.makeRequest("POST", "auth/sign-in", payload, "")
	if err != nil {
		return nil, err
	}

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Token string `json:"token"`
			User  struct {
				Name  string `json:"name"`
				Email string `json:"email"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 200 {
		return nil, fmt.Errorf("login failed: %s", resp.Message)
	}

	return &BikaUserToken{
		Token:   resp.Data.Token,
		Name:    resp.Data.User.Name,
		Email:   resp.Data.User.Email,
		Expires: time.Now().Add(7 * 24 * time.Hour), // Token 有效期约7天
	}, nil
}

func (b *BikaClient) Search(keyword string, page int, token string) ([]BikaSearchResult, int, error) {
	payload := map[string]interface{}{
		"keyword": keyword,
	}
	endpoint := fmt.Sprintf("comics/advanced-search?page=%d", page)

	respBody, err := b.makeRequest("POST", endpoint, payload, token)
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

func (b *BikaClient) GetComicDetail(comicID, token string) (*BikaComic, error) {
	endpoint := fmt.Sprintf("comics/%s", comicID)
	respBody, err := b.makeRequest("GET", endpoint, nil, token)
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

func (b *BikaClient) GetChapters(comicID string, page int, token string) ([]BikaChapter, int, error) {
	endpoint := fmt.Sprintf("comics/%s/eps?page=%d", comicID, page)
	respBody, err := b.makeRequest("GET", endpoint, nil, token)
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

func (b *BikaClient) GetChapterImages(comicID string, order, page int, token string) ([]BikaPageItem, int, error) {
	endpoint := fmt.Sprintf("comics/%s/order/%d/pages?page=%d", comicID, order, page)
	respBody, err := b.makeRequest("GET", endpoint, nil, token)
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

func (b *BikaClient) DownloadChapter(ctx context.Context, comicID, comicTitle, epTitle string, epOrder int, outputDir, token string, quality string) (string, error) {
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
		pages, total, err := b.GetChapterImages(comicID, epOrder, page, token)
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

			if err := downloadBikaFile(imageURL, filename, token, quality); err != nil {
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

	// 收集所有图片文件并排序
	var imageFiles []string
	entries, err := os.ReadDir(epDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				name := e.Name()
				ext := strings.ToLower(filepath.Ext(name))
				if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" {
					imageFiles = append(imageFiles, filepath.Join(epDir, name))
				}
			}
		}
	}
	sort.Strings(imageFiles)

	// 生成CBZ并保留
	cbzPath := filepath.Join(filepath.Dir(epDir), comicTitle+".cbz")
	if err := zipDirToCBZ(epDir, cbzPath); err != nil {
		log.Printf("bika cbz build failed: %v", err)
	}

	// 生成PDF用于发送
	pdfPath := filepath.Join(filepath.Dir(epDir), comicTitle+".pdf")
	if len(imageFiles) > 0 {
		if err := buildPDF(pdfPath, imageFiles, ""); err != nil {
			log.Printf("bika pdf build failed: %v", err)
			os.RemoveAll(epDir)
			return "", fmt.Errorf("pdf build failed: %v", err)
		}
		os.RemoveAll(epDir)
		return pdfPath, nil
	}

	os.RemoveAll(epDir)
	return "", fmt.Errorf("no images found")
}

func buildBikaImageURL(fileServer, path string) string {
	// 去掉图片处理参数，获取原画
	// 原始path: tobeimg/xxx/rs:fit:800:800:0/g:ce/base64url.jpg
	// 原画path: tobeimg/xxx/g:ce/base64url.jpg 或 tobeimg/xxx/base64url.jpg
	originalPath := path
	if idx := strings.Index(path, "/rs:"); idx >= 0 {
		path = path[:idx] + path[strings.Index(path[idx:], "/"):]
	}
	
	// 添加日志
	if originalPath != path {
		log.Printf("[Bika] 图片URL处理: 原始=%s, 处理后=%s", originalPath[:50]+"...", path[:50]+"...")
	}
	
	if strings.Contains(fileServer, "go2778") || strings.Contains(fileServer, "static") {
		return fileServer + "/static/" + path
	}
	directURL := fileServer + "/static/" + path
	return strings.ReplaceAll(directURL, "picacomic", "go2778")
}

func downloadBikaFile(url, filepath, token string, quality string) error {
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

	// 设置画质
	if quality != "" {
		req.Header.Set("image-quality", quality)
	} else {
		req.Header.Set("image-quality", "original")
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

func getBikaConfig(cfg *Config) BikaConfig {
	return BikaConfig{
		Enabled: cfg.BikaEnabled,
		BaseURL: cfg.BikaBaseURL,
		Token:   cfg.BikaToken,
		Quality: cfg.BikaQuality,
		Proxy:   cfg.BikaProxy,
	}
}

func (a *App) getBikaUserToken(userID int64) string {
	bikaUserTokensMu.RLock()
	defer bikaUserTokensMu.RUnlock()

	// 优先使用用户自己的 token
	if token, ok := bikaUserTokens[userID]; ok {
		if time.Now().Before(token.Expires) {
			return token.Token
		}
	}

	// 如果用户没有 token，使用全局 token（管理员已登录的）
	cfg := a.currentConfig()
	if cfg.BikaEnabled && cfg.BikaToken != "" {
		return cfg.BikaToken
	}

	return ""
}

func (a *App) handleBikaCommand(rawMessage, messageType string, groupID, userID int64, scope string, data map[string]any) bool {
	// 只处理 /bika 开头的命令
	if !strings.HasPrefix(strings.TrimSpace(rawMessage), "/bika") {
		return false
	}

	cfg := a.currentConfig()

	// 检查 bika 是否启用
	if !cfg.BikaEnabled {
		// 只有管理员可以启用
		if matched(`^/bika\s+on$`, rawMessage) {
			if !a.requireAdmin(messageType, groupID, userID, "仅管理员可启用哔咔") {
				return true
			}
			a.cfgMu.Lock()
			a.cfg.BikaEnabled = true
			a.cfgMu.Unlock()
			a.saveConfig()
			a.bika = NewBikaClient(getBikaConfig(a.cfg))
			a.sendMessage(messageType, groupID, userID, "哔咔已启用")
			return true
		}
		return false
	}

	// /bika off - 关闭哔咔
	if matched(`^/bika\s+off$`, rawMessage) {
		if !a.requireAdmin(messageType, groupID, userID, "仅管理员可关闭哔咔") {
			return true
		}
		a.cfgMu.Lock()
		a.cfg.BikaEnabled = false
		a.cfgMu.Unlock()
		a.saveConfig()
		a.bika = nil
		a.sendMessage(messageType, groupID, userID, "哔咔已关闭")
		return true
	}

	if a.bika == nil {
		return false
	}

	// /bika login <email> <password>
	if m := mustMatch(`^/bika\s+login\s+(\S+)\s+(\S+)$`, rawMessage); m != nil {
		email := m[1]
		password := m[2]

		// 非管理员只能登录自己的账号
		if userID != cfg.AdminID {
			// 检查全局是否已开启（管理员已登录）
			bikaUserTokensMu.RLock()
			adminToken, hasAdmin := bikaUserTokens[cfg.AdminID]
			bikaUserTokensMu.RUnlock()

			if !hasAdmin || adminToken == nil || time.Now().After(adminToken.Expires) {
				a.sendMessage(messageType, groupID, userID, "哔咔未全局开启，请等待管理员登录后再试")
				return true
			}
		}

		a.sendMessage(messageType, groupID, userID, "正在登录哔咔...")

		token, err := a.bika.SignIn(email, password)
		if err != nil {
			a.sendMessage(messageType, groupID, userID, "登录失败："+err.Error())
			return true
		}

		bikaUserTokensMu.Lock()
		bikaUserTokens[userID] = token
		bikaUserTokensMu.Unlock()

		// 如果是管理员登录，自动设置全局 token
		if userID == cfg.AdminID {
			a.cfgMu.Lock()
			a.cfg.BikaToken = token.Token
			a.cfgMu.Unlock()
			a.saveConfig()
			a.bika.token = token.Token
			a.sendMessage(messageType, groupID, userID, fmt.Sprintf("哔咔管理员登录成功：%s\n已设置为全局默认账号", token.Name))
		} else {
			a.sendMessage(messageType, groupID, userID, fmt.Sprintf("哔咔登录成功：%s", token.Name))
		}
		return true
	}

	// /bika logout
	if matched(`^/bika\s+logout$`, rawMessage) {
		bikaUserTokensMu.Lock()
		delete(bikaUserTokens, userID)
		bikaUserTokensMu.Unlock()

		if userID == cfg.AdminID {
			a.cfgMu.Lock()
			a.cfg.BikaToken = ""
			a.cfgMu.Unlock()
			a.saveConfig()
			a.bika.token = ""
			a.sendMessage(messageType, groupID, userID, "已退出哔咔管理员账号，其他用户将无法使用")
		} else {
			a.sendMessage(messageType, groupID, userID, "已退出哔咔账号")
		}
		return true
	}

	// /bika whoami
	if matched(`^/bika\s+whoami$`, rawMessage) {
		bikaUserTokensMu.RLock()
		token, ok := bikaUserTokens[userID]
		bikaUserTokensMu.RUnlock()

		if ok && time.Now().Before(token.Expires) {
			a.sendMessage(messageType, groupID, userID, fmt.Sprintf("当前哔咔账号：%s (%s)", token.Name, token.Email))
		} else {
			a.sendMessage(messageType, groupID, userID, "未登录哔咔账号，使用全局账号")
		}
		return true
	}

	// /bika help
	if matched(`^/bika\s+help$`, rawMessage) {
		msg := "哔咔漫画源命令：\n" +
			"1) /bika login <邮箱> <密码>：登录哔咔账号\n" +
			"2) /bika logout：退出当前账号\n" +
			"3) /bika whoami：查看当前登录状态\n" +
			"4) /bika search <关键词>：搜索漫画\n" +
			"5) /bika look <ID>：查看漫画详情\n" +
			"6) /bika dl <ID> [章节]：下载漫画\n" +
			"7) /bika confirm <序号>：确认搜索结果下载\n" +
			"8) /bika on/off：启用/关闭哔咔（管理员）"
		a.sendMessage(messageType, groupID, userID, msg)
		return true
	}

	// 以下命令需要登录（有 token）
	token := a.getBikaUserToken(userID)
	if token == "" && userID != cfg.AdminID {
		a.sendMessage(messageType, groupID, userID, "请先登录哔咔：/bika login <邮箱> <密码>")
		return true
	}

	// /bika search <keyword>
	if m := mustMatch(`^/bika\s+search\s+(.+)$`, rawMessage); m != nil {
		keyword := strings.TrimSpace(m[1])
		if keyword == "" {
			a.sendMessage(messageType, groupID, userID, "请输入搜索关键词")
			return true
		}
		a.sendMessage(messageType, groupID, userID, "正在哔咔搜索："+keyword+" ...")

		results, total, err := a.bika.Search(keyword, 1, token)
		if err != nil {
			a.sendMessage(messageType, groupID, userID, "哔咔搜索失败："+err.Error())
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
			lines = append(lines, fmt.Sprintf("%d. %s\n   作者：%s 标签：%s", i+1, r.Title, r.Author, tags))
		}
		msg := fmt.Sprintf("哔咔搜索结果（共%d条）：\n%s\n\n回复 确认 <序号> 下载", total, strings.Join(lines, "\n"))
		a.sendRecordMessage(messageType, groupID, userID, msg)
		return true
	}

	// /bika confirm <index> 或 确认 <index> (支持多个序号，如: 确认 1 2 3)
	if m := mustMatch(`^(?:/bika\s+confirm|确认)\s+(.+)$`, rawMessage); m != nil {
		// 解析所有序号
		parts := strings.Fields(m[1])
		var indices []int
		for _, p := range parts {
			idx, err := strconv.Atoi(p)
			if err != nil || idx <= 0 {
				continue
			}
			indices = append(indices, idx)
		}
		if len(indices) == 0 {
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

		// 检查序号范围
		var validComics []BikaSearchResult
		for _, idx := range indices {
			if idx > len(pending.Results) {
				a.sendMessage(messageType, groupID, userID, fmt.Sprintf("序号 %d 超出范围，最大为 %d", idx, len(pending.Results)))
				continue
			}
			validComics = append(validComics, pending.Results[idx-1])
		}

		if len(validComics) == 0 {
			return true
		}

		// 清除搜索缓存
		bikaSearchCacheMu.Lock()
		delete(bikaSearchCache, scope)
		bikaSearchCacheMu.Unlock()

		// 逐个下载
		if len(validComics) == 1 {
			comic := validComics[0]
			a.sendMessage(messageType, groupID, userID, fmt.Sprintf("开始下载哔咔漫画：%s", comic.Title))
			go a.bikaDownloadAndSend(comic.ID, "", messageType, groupID, userID)
		} else {
			names := make([]string, len(validComics))
			for i, c := range validComics {
				names[i] = c.Title
			}
			a.sendMessage(messageType, groupID, userID, fmt.Sprintf("开始下载 %d 个哔咔漫画：\n%s", len(validComics), strings.Join(names, "\n")))
			for _, comic := range validComics {
				go a.bikaDownloadAndSend(comic.ID, "", messageType, groupID, userID)
			}
		}
		return true
	}

	// /bika look <id>
	if m := mustMatch(`^/bika\s+look\s+([a-f0-9]+)$`, rawMessage); m != nil {
		comicID := m[1]
		a.sendMessage(messageType, groupID, userID, "正在查询哔咔漫画："+comicID)

		comic, err := a.bika.GetComicDetail(comicID, token)
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
		a.sendMessage(messageType, groupID, userID, fmt.Sprintf("开始下载哔咔漫画 ID: %s", comicID))
		go a.bikaDownloadAndSend(comicID, chapter, messageType, groupID, userID)
		return true
	}

	return false
}

func (a *App) bikaDownloadAndSend(comicID, chapterStr string, messageType string, groupID, userID int64) {
	token := a.getBikaUserToken(userID)

	comic, err := a.bika.GetComicDetail(comicID, token)
	if err != nil {
		a.sendMessage(messageType, groupID, userID, "获取漫画信息失败："+err.Error())
		return
	}

	chapters, _, err := a.bika.GetChapters(comicID, 1, token)
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

	// 获取画质配置
	quality := cfg.BikaQuality
	if quality == "" {
		quality = "original"
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

	// 并发下载章节
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3) // 最多3个并发
	successCount := 0
	var mu sync.Mutex

	for _, ch := range toDownload {
		wg.Add(1)
		sem <- struct{}{}
		go func(ch BikaChapter) {
			defer wg.Done()
			defer func() { <-sem }()

			// 下载并生成PDF
			var result string
			var err error
			for retry := 0; retry < 3; retry++ {
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.DownloadTimeout)*time.Second)
				result, err = a.bika.DownloadChapter(ctx, comicID, comic.Title, ch.Title, ch.Order, outputDir, token, quality)
				cancel()

				if err == nil {
					break
				}
				log.Printf("bika download chapter %d retry %d failed: %v", ch.Order, retry+1, err)
			}

			if err != nil {
				log.Printf("bika download chapter %d failed after 3 retries: %v", ch.Order, err)
				a.sendMessage(messageType, groupID, userID, fmt.Sprintf("下载失败：%s 第%d话 - %v", comic.Title, ch.Order, err))
				a.notifyAdminDownloadFailure(groupID, comicID, fmt.Sprintf("哔咔下载失败: %s 第%d话 - %v", comic.Title, ch.Order, err))
				return
			}

			mu.Lock()
			successCount++
			mu.Unlock()

			// 使用转发消息发送（包含基本信息、封面、文件）
			infoMsg := fmt.Sprintf("车牌号：%s\n本子名：%s\n来源：Bika\n章节：%d话 %s\n文件类型：PDF", comicID, comic.Title, ch.Order, ch.Title)
			coverPath := ""
			if mangaPath, ok, _ := a.findMangaPageByID("", 1); ok && fileExists(mangaPath) {
				coverPath = mangaPath
			}
			sendOK := a.sendComicForwardMessage(messageType, groupID, userID, infoMsg, coverPath, result, cfg)
			if !sendOK {
				// 回退到普通发送
				for retry := 0; retry < 3; retry++ {
					if messageType == "group" {
						sendOK = a.bot.SendGroupFile(cfg, groupID, result)
					} else {
						sendOK = a.bot.SendPrivateFile(cfg, userID, result)
					}
					if sendOK {
						break
					}
					log.Printf("bika send file retry %d: %s ch%d", retry+1, comic.Title, ch.Order)
					time.Sleep(2 * time.Second)
				}
			}

			if !sendOK {
				log.Printf("bika send file failed after 3 retries: %s ch%d", comic.Title, ch.Order)
				a.sendMessage(messageType, groupID, userID, fmt.Sprintf("文件发送失败：%s 第%d话", comic.Title, ch.Order))
				a.notifyAdminSendFailure(groupID, comicID, comic.Title, result)
			}

			// 发送成功后删除PDF
			if sendOK {
				_ = os.Remove(result)
			}
		}(ch)
	}

	wg.Wait()
	if successCount > 0 {
		a.sendMessage(messageType, groupID, userID, fmt.Sprintf("哔咔下载完成：%s（%d话）", comic.Title, successCount))
	} else {
		a.sendMessage(messageType, groupID, userID, fmt.Sprintf("哔咔下载失败：%s", comic.Title))
	}
}

// bikaDownloadComic 下载哔咔漫画并返回CBZ文件路径（用于升级策略）
func (a *App) bikaDownloadComic(ctx context.Context, comicID, chapterStr string, messageType string, groupID, userID int64, token string, quality string) (string, error) {
	comic, err := a.bika.GetComicDetail(comicID, token)
	if err != nil {
		return "", fmt.Errorf("get comic detail failed: %v", err)
	}

	chapters, _, err := a.bika.GetChapters(comicID, 1, token)
	if err != nil {
		return "", fmt.Errorf("get chapters failed: %v", err)
	}

	if len(chapters) == 0 {
		return "", fmt.Errorf("no chapters found")
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
			return "", fmt.Errorf("invalid chapter number")
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
			return "", fmt.Errorf("chapter %d not found", chapterNum)
		}
	} else {
		if len(chapters) > cfg.MaxEpisodes {
			return "", fmt.Errorf("too many chapters (%d>%d)", len(chapters), cfg.MaxEpisodes)
		}
		toDownload = chapters
	}

	// 单章节直接下载
	if len(toDownload) == 1 {
		chCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.DownloadTimeout)*time.Second)
		result, err := a.bika.DownloadChapter(chCtx, comicID, comic.Title, toDownload[0].Title, toDownload[0].Order, outputDir, token, quality)
		cancel()
		if err != nil {
			return "", fmt.Errorf("download chapter failed: %v", err)
		}
		return result, nil
	}

	// 多章节：下载所有章节后合并成一个CBZ
	comicTitle := sanitizeBikaFilename(strings.TrimSpace(comic.Title))
	mergeDir := filepath.Join(outputDir, fmt.Sprintf("bika_%s", comicID), comicTitle)
	if err := os.MkdirAll(mergeDir, 0755); err != nil {
		return "", fmt.Errorf("create merge dir failed: %v", err)
	}

	successCount := 0
	for _, ch := range toDownload {
		_, cancel := context.WithTimeout(ctx, time.Duration(cfg.DownloadTimeout)*time.Second)
		epTitle := sanitizeBikaFilename(strings.TrimSpace(ch.Title))
		chapterDir := filepath.Join(mergeDir, fmt.Sprintf("%03d_%s", ch.Order, epTitle))

		// 下载章节图片
		page := 1
		for {
			pages, total, imgErr := a.bika.GetChapterImages(comicID, ch.Order, page, token)
			if imgErr != nil || len(pages) == 0 {
				break
			}
			if err := os.MkdirAll(chapterDir, 0755); err != nil {
				break
			}
			for idx, p := range pages {
				imageURL := buildBikaImageURL(p.Media.FileServer, p.Media.Path)
				filename := filepath.Join(chapterDir, fmt.Sprintf("%03d_%s", idx+1, p.Media.OriginalName))
				_ = downloadBikaFile(imageURL, filename, token, quality)
			}
			if page >= total {
				break
			}
			page++
		}
		cancel()

		// 检查是否有图片
		if entries, err := os.ReadDir(chapterDir); err == nil && len(entries) > 0 {
			successCount++
		}
	}

	if successCount == 0 {
		os.RemoveAll(mergeDir)
		return "", fmt.Errorf("all chapters download failed")
	}

	// 收集所有图片文件并排序
	var allImageFiles []string
	filepath.Walk(mergeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" {
			allImageFiles = append(allImageFiles, path)
		}
		return nil
	})
	sort.Strings(allImageFiles)

	// 生成PDF
	if len(allImageFiles) > 0 {
		pdfPath := filepath.Join(outputDir, fmt.Sprintf("bika_%s_%s.pdf", comicID, comicTitle))
		if err := buildPDF(pdfPath, allImageFiles, ""); err != nil {
			log.Printf("merge pdf failed: %v", err)
			// 回退到CBZ
			cbzPath := filepath.Join(outputDir, fmt.Sprintf("bika_%s_%s.cbz", comicID, comicTitle))
			if err := zipDirToCBZ(mergeDir, cbzPath); err != nil {
				return mergeDir, nil
			}
			os.RemoveAll(mergeDir)
			return cbzPath, nil
		}
		os.RemoveAll(mergeDir)
		return pdfPath, nil
	}

	// 回退到CBZ
	cbzPath := filepath.Join(outputDir, fmt.Sprintf("bika_%s_%s.cbz", comicID, comicTitle))
	if err := zipDirToCBZ(mergeDir, cbzPath); err != nil {
		return mergeDir, nil
	}
	os.RemoveAll(mergeDir)
	return cbzPath, nil
}

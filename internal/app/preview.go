package app

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type previewBook struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Path    string    `json:"-"`
}

type previewMetaResp struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	PageCount int    `json:"page_count"`
	Download  string `json:"download"`
}

var (
	jmIDInNameRe  = regexp.MustCompile(`(?i)jm[\s_-]*([0-9]{3,})`)
	plainIDNameRe = regexp.MustCompile(`(?:^|[^0-9])([0-9]{5,})(?:[^0-9]|$)`)

	previewBooksCacheMu sync.RWMutex
	previewBooksCache   []previewBook
	previewBooksCacheAt time.Time

	previewMangaCacheMu sync.RWMutex
	previewMangaCache   map[string]previewMangaPages

	previewCBZPageCacheMu sync.Mutex
	previewCBZPageCache   = map[string]previewCBZPage{}
)

type previewMangaPages struct {
	Pages     []string
	ExpiresAt time.Time
}

type previewCBZPage struct {
	Raw       []byte
	Ext       string
	ExpiresAt time.Time
}

func (a *App) registerPreviewRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/search", a.handlePreviewSearch)
	mux.HandleFunc("/api/comic/", a.handlePreviewComicAPI)
	mux.HandleFunc("/", a.handlePreviewPage)
}

func (a *App) handlePreviewPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	p := strings.Trim(r.URL.Path, "/")
	if p == "" {
		writeHTML(w, previewHomeHTML())
		return
	}
	if id, ok := parseJMPathID(p); ok {
		writeHTML(w, previewViewerHTML(id))
		return
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("not found"))
}

func (a *App) handlePreviewSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	books, err := a.listPreviewBooks()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if q != "" {
		lq := strings.ToLower(q)
		filtered := make([]previewBook, 0, len(books))
		for _, b := range books {
			if strings.Contains(strings.ToLower(b.ID), lq) || strings.Contains(strings.ToLower(b.Title), lq) || strings.Contains(strings.ToLower(b.Name), lq) {
				filtered = append(filtered, b)
			}
		}
		books = filtered
	}
	if len(books) > 100 {
		books = books[:100]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": books})
}

func (a *App) handlePreviewComicAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	remain := strings.TrimPrefix(r.URL.Path, "/api/comic/")
	parts := strings.Split(strings.Trim(remain, "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	id := normalizeJMID(parts[0])
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid id"))
		return
	}
	book, hasCBZ, err := a.findBookByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	if len(parts) >= 2 && parts[1] == "download" {
		if !hasCBZ {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "cbz not found"})
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(book.Path)))
		http.ServeFile(w, r, book.Path)
		return
	}

	if len(parts) >= 3 && parts[1] == "page" {
		pageNo, err := strconv.Atoi(parts[2])
		if err != nil || pageNo <= 0 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("invalid page"))
			return
		}
		if mangaPath, ok, err := a.findMangaPageByID(id, pageNo); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(err.Error()))
			return
		} else if ok {
			w.Header().Set("Cache-Control", "public, max-age=300")
			http.ServeFile(w, r, mangaPath)
			return
		}
		if !hasCBZ {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("comic not found"))
			return
		}
		raw, ext, err := readCBZPage(book.Path, pageNo)
		if err != nil {
			if strings.Contains(err.Error(), "out of range") {
				w.WriteHeader(http.StatusNotFound)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			_, _ = w.Write([]byte(err.Error()))
			return
		}
		ct := mime.TypeByExtension(ext)
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(raw)
		return
	}

	pageCount, hasManga, err := a.countMangaPagesByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !hasManga {
		if !hasCBZ {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "comic not found"})
			return
		}
		pageCount, err = countCBZPages(book.Path)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
	}
	title := "JM" + id
	download := ""
	if hasCBZ {
		title = book.Title
		download = fmt.Sprintf("/api/comic/%s/download", id)
	}
	writeJSON(w, http.StatusOK, previewMetaResp{
		ID:        id,
		Title:     title,
		PageCount: pageCount,
		Download:  download,
	})
}

func (a *App) listPreviewBooks() ([]previewBook, error) {
	previewBooksCacheMu.RLock()
	if time.Since(previewBooksCacheAt) < 5*time.Second && len(previewBooksCache) > 0 {
		out := make([]previewBook, len(previewBooksCache))
		copy(out, previewBooksCache)
		previewBooksCacheMu.RUnlock()
		return out, nil
	}
	previewBooksCacheMu.RUnlock()

	cfg := a.currentConfig()
	root := strings.TrimSpace(cfg.CBZDir)
	if root == "" {
		root = "./cbz/"
	}
	entries := make([]previewBook, 0, 64)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".cbz") {
			return nil
		}
		id := extractIDFromName(d.Name())
		if id == "" {
			return nil
		}
		st, stErr := os.Stat(path)
		if stErr != nil {
			return nil
		}
		entries = append(entries, previewBook{
			ID:      id,
			Title:   deriveTitleFromName(d.Name(), id),
			Name:    d.Name(),
			Size:    st.Size(),
			ModTime: st.ModTime(),
			Path:    path,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	best := map[string]previewBook{}
	for _, b := range entries {
		cur, ok := best[b.ID]
		if !ok || scorePreviewBook(b) > scorePreviewBook(cur) {
			best[b.ID] = b
		}
	}
	out := make([]previewBook, 0, len(best))
	for _, b := range best {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModTime.After(out[j].ModTime)
	})

	previewBooksCacheMu.Lock()
	previewBooksCache = make([]previewBook, len(out))
	copy(previewBooksCache, out)
	previewBooksCacheAt = time.Now()
	previewBooksCacheMu.Unlock()

	return out, nil
}

func (a *App) findBookByID(id string) (previewBook, bool, error) {
	books, err := a.listPreviewBooks()
	if err != nil {
		return previewBook{}, false, err
	}
	for _, b := range books {
		if b.ID == id {
			return b, true, nil
		}
	}
	return previewBook{}, false, nil
}

func extractIDFromName(name string) string {
	if m := jmIDInNameRe.FindStringSubmatch(name); len(m) > 1 {
		return normalizeJMID(m[1])
	}
	if m := plainIDNameRe.FindStringSubmatch(name); len(m) > 1 {
		return normalizeJMID(m[1])
	}
	return ""
}

func normalizeJMID(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "jm")
	re := regexp.MustCompile(`^[0-9]{3,}$`)
	if !re.MatchString(s) {
		return ""
	}
	return s
}

func parseJMPathID(pathVal string) (string, bool) {
	p := strings.Split(strings.TrimSpace(pathVal), "/")[0]
	id := normalizeJMID(p)
	return id, id != ""
}

func deriveTitleFromName(name, id string) string {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	re := regexp.MustCompile(`(?i)^jm[\s_-]*` + regexp.QuoteMeta(id) + `[\s_-]*`)
	base = re.ReplaceAllString(base, "")
	base = strings.TrimSpace(base)
	if base == "" {
		base = "JM" + id
	}
	return base
}

func scorePreviewBook(b previewBook) int {
	score := 0
	lower := strings.ToLower(b.Name)
	if !strings.Contains(lower, "_ch") && !strings.Contains(lower, "ch00") && !strings.Contains(lower, "ch0") {
		score += 20
	}
	if strings.HasPrefix(lower, "jm"+b.ID+"_") {
		score += 10
	}
	if b.Size > 0 {
		score += int(b.Size / (1024 * 1024))
	}
	return score
}

func countCBZPages(cbzPath string) (int, error) {
	r, err := zip.OpenReader(cbzPath)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	return len(collectImageEntries(r.File)), nil
}

func readCBZPage(cbzPath string, pageNo int) ([]byte, string, error) {
	if pageNo > 0 {
		if raw, ext, ok := getCachedCBZPage(cbzPath, pageNo); ok {
			return raw, ext, nil
		}
	}
	r, err := zip.OpenReader(cbzPath)
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	imgs := collectImageEntries(r.File)
	if pageNo <= 0 || pageNo > len(imgs) {
		return nil, "", fmt.Errorf("page out of range")
	}
	target := imgs[pageNo-1]
	rc, err := target.Open()
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil, "", err
	}
	ext := strings.ToLower(filepath.Ext(target.Name))
	setCachedCBZPage(cbzPath, pageNo, raw, ext)
	return raw, ext, nil
}

func collectImageEntries(files []*zip.File) []*zip.File {
	out := make([]*zip.File, 0, len(files))
	for _, f := range files {
		if f.FileInfo().IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".avif":
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func getCachedCBZPage(cbzPath string, pageNo int) ([]byte, string, bool) {
	key := fmt.Sprintf("%s|%d", cbzPath, pageNo)
	now := time.Now()
	previewCBZPageCacheMu.Lock()
	defer previewCBZPageCacheMu.Unlock()
	item, ok := previewCBZPageCache[key]
	if !ok || now.After(item.ExpiresAt) {
		delete(previewCBZPageCache, key)
		return nil, "", false
	}
	return item.Raw, item.Ext, true
}

func setCachedCBZPage(cbzPath string, pageNo int, raw []byte, ext string) {
	key := fmt.Sprintf("%s|%d", cbzPath, pageNo)
	cp := make([]byte, len(raw))
	copy(cp, raw)

	previewCBZPageCacheMu.Lock()
	defer previewCBZPageCacheMu.Unlock()
	previewCBZPageCache[key] = previewCBZPage{
		Raw:       cp,
		Ext:       ext,
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}
	if len(previewCBZPageCache) <= 256 {
		return
	}
	now := time.Now()
	for k, v := range previewCBZPageCache {
		if now.After(v.ExpiresAt) {
			delete(previewCBZPageCache, k)
		}
	}
	if len(previewCBZPageCache) > 256 {
		previewCBZPageCache = map[string]previewCBZPage{}
	}
}

func (a *App) countMangaPagesByID(id string) (int, bool, error) {
	pages, ok, err := a.listMangaPagesByID(id)
	if err != nil {
		return 0, false, err
	}
	return len(pages), ok && len(pages) > 0, nil
}

func (a *App) findMangaPageByID(id string, pageNo int) (string, bool, error) {
	pages, ok, err := a.listMangaPagesByID(id)
	if err != nil {
		return "", false, err
	}
	if !ok || pageNo <= 0 || pageNo > len(pages) {
		return "", false, nil
	}
	return pages[pageNo-1], true, nil
}

func (a *App) listMangaPagesByID(id string) ([]string, bool, error) {
	cfg := a.currentConfig()
	root := strings.TrimSpace(cfg.MangaDir)
	if root == "" {
		root = "./manga/"
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	bestDir := ""
	var bestModTime time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		dirID := extractIDFromName(name)
		if dirID != id {
			continue
		}
		st, stErr := os.Stat(filepath.Join(root, name))
		if stErr != nil {
			continue
		}
		if bestDir == "" || st.ModTime().After(bestModTime) {
			bestDir = filepath.Join(root, name)
			bestModTime = st.ModTime()
		}
	}
	if bestDir == "" {
		return nil, false, nil
	}

	cacheKey := bestDir
	now := time.Now()
	previewMangaCacheMu.RLock()
	if previewMangaCache != nil {
		if cached, ok := previewMangaCache[cacheKey]; ok && now.Before(cached.ExpiresAt) {
			pages := make([]string, len(cached.Pages))
			copy(pages, cached.Pages)
			previewMangaCacheMu.RUnlock()
			return pages, true, nil
		}
	}
	previewMangaCacheMu.RUnlock()

	pages := make([]string, 0, 256)
	err = filepath.WalkDir(bestDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if isImageExt(filepath.Ext(d.Name())) {
			pages = append(pages, path)
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.Strings(pages)

	previewMangaCacheMu.Lock()
	if previewMangaCache == nil {
		previewMangaCache = map[string]previewMangaPages{}
	}
	previewMangaCache[cacheKey] = previewMangaPages{
		Pages:     pages,
		ExpiresAt: now.Add(30 * time.Second),
	}
	previewMangaCacheMu.Unlock()

	out := make([]string, len(pages))
	copy(out, pages)
	return out, true, nil
}

func isImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".avif":
		return true
	default:
		return false
	}
}

func writeHTML(w http.ResponseWriter, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

func previewHomeHTML() string {
	return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>JM 本地预览</title>
<style>
body{margin:0;background:#0f1115;color:#e6e8eb;font-family:ui-sans-serif,system-ui,-apple-system;padding:20px}
.wrap{max-width:980px;margin:0 auto}
input{width:100%;padding:12px 14px;border-radius:10px;border:1px solid #2a2f38;background:#131722;color:#fff;font-size:16px}
.list{margin-top:16px;display:grid;gap:10px}
.item{padding:12px;border:1px solid #2a2f38;border-radius:10px;background:#131722;display:flex;justify-content:space-between;gap:8px}
a{color:#8ab4ff;text-decoration:none}
.meta{opacity:.75;font-size:12px}
</style>
</head>
<body>
<div class="wrap">
  <h2>JM 本地 CBZ 预览</h2>
  <input id="q" placeholder="输入 JM 号或标题关键词，例如 350234" />
  <div id="list" class="list"></div>
</div>
<script>
const q = document.getElementById('q');
const list = document.getElementById('list');
async function load(){
  const kw = encodeURIComponent(q.value.trim());
  const r = await fetch('/api/search?q=' + kw);
  const data = await r.json();
  const items = (data.items || []);
  list.innerHTML = items.map(function(it){
    return '<div class="item">' +
      '<div>' +
      '<div><a href="/' + it.id + '">JM' + it.id + '</a> - ' + it.title + '</div>' +
      '<div class="meta">' + it.name + ' · ' + (it.size/1024/1024).toFixed(2) + 'MB</div>' +
      '</div>' +
      '<div><a href="/api/comic/' + it.id + '/download">下载</a></div>' +
      '</div>';
  }).join('');
}
q.addEventListener('input', () => load());
load();
</script>
</body>
</html>`
}

func previewViewerHTML(id string) string {
	page := map[string]string{"id": id}
	b, _ := json.Marshal(page)
	return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>JM` + id + ` 预览</title>
<style>
body{margin:0;background:#0b0d10;color:#eef2f6;font-family:ui-sans-serif,system-ui,-apple-system}
.bar{position:fixed;left:0;right:0;top:0;height:52px;background:#121722;border-bottom:1px solid #2e3644;display:flex;align-items:center;padding:0 10px;gap:8px;z-index:10}
.btn{background:#1f2633;color:#fff;border:1px solid #354056;border-radius:8px;padding:7px 10px;cursor:pointer}
.title{flex:1;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.viewer{padding-top:56px;height:calc(100vh - 56px);display:flex;align-items:center;justify-content:center}
img{max-width:100%;max-height:calc(100vh - 68px);object-fit:contain}
</style>
</head>
<body>
<div class="bar">
  <button class="btn" id="back">返回</button>
  <button class="btn" id="prev">上一页</button>
  <button class="btn" id="next">下一页</button>
  <button class="btn" id="fullscreen">全屏</button>
  <a class="btn" id="download" href="#">下载</a>
  <div class="title" id="title">加载中...</div>
</div>
<div class="viewer"><img id="img" alt="page"/></div>
<script>
const state = ` + string(b) + `;
let page = 1;
let total = 1;
const img = document.getElementById('img');
const title = document.getElementById('title');
async function init(){
  const r = await fetch('/api/comic/' + state.id);
  if (!r.ok) {
    title.textContent = '未找到本地 CBZ：JM' + state.id;
    return;
  }
  const meta = await r.json();
  total = Math.max(1, meta.page_count || 1);
  const dl = document.getElementById('download');
  if (meta.download) {
    dl.href = meta.download;
    dl.style.display = '';
  } else {
    dl.style.display = 'none';
  }
  title.textContent = 'JM' + meta.id + ' - ' + meta.title;
  render();
}
function render(){
  if (page < 1) page = 1;
  if (page > total) page = total;
  img.src = '/api/comic/' + state.id + '/page/' + page;
  title.textContent = title.textContent.split(' ｜ ')[0] + ' ｜ 第 ' + page + ' / ' + total + ' 页';
}
document.getElementById('prev').onclick = () => { page--; render(); };
document.getElementById('next').onclick = () => { page++; render(); };
document.getElementById('back').onclick = () => { location.href = '/'; };
document.getElementById('fullscreen').onclick = async () => {
  if (!document.fullscreenElement) await document.documentElement.requestFullscreen();
  else await document.exitFullscreen();
};
window.addEventListener('keydown', (e) => {
  if (e.key === 'ArrowLeft' || e.key.toLowerCase() === 'a') { page--; render(); }
  if (e.key === 'ArrowRight' || e.key.toLowerCase() === 'd') { page++; render(); }
  if (e.key.toLowerCase() === 'f') document.getElementById('fullscreen').click();
});
img.addEventListener('click', () => { page++; render(); });
init();
</script>
</body>
</html>`
}

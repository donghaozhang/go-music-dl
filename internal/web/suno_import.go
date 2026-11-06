package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/guohuiyuan/go-music-dl/core"
)

// This adapter uses UseSuno's current anonymous public-page protocol. It does
// not request Suno credentials, spend official credits, or decrypt media.
// If a public downloadable video/audio is unavailable, fail explicitly.
var sunoImportMu sync.Mutex
var sunoImportRunner = importSunoSong
var sunoImportClient = &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
	if len(via) >= 3 || !allowedSunoMediaURL(r.URL.String()) {
		return errors.New("不支持的媒体跳转")
	}
	return nil
}}
var sunoTimedLyrics = regexp.MustCompile(`(?m)^\[\d{1,3}:\d{2}(?:[.:]\d+)?\]`)

type sunoImportResult struct {
	ID        string `json:"song_id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	LyricPath string `json:"lyric_path,omitempty"`
	Lyrics    string `json:"lyrics,omitempty"`
	LyricKind string `json:"lyric_kind"`
	Existing  bool   `json:"existing"`
}

type sunoClientState struct {
	BrowserID string `json:"browser_id"`
	Identity  string `json:"identity,omitempty"`
}

func RegisterSunoImportRoutes(api *gin.RouterGroup) {
	api.POST("/suno/import", func(c *gin.Context) {
		// Import is a local workstation feature. Do not trust forwarded ClientIP.
		host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || !allowSameOriginWrite(c) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Suno 导入仅允许从本机播放器操作"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		var req struct {
			URL string `json:"url"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(400, gin.H{"error": "请输入歌曲链接"})
			return
		}
		canonical, id, err := normalizeSunoURL(req.URL)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		if !sunoImportMu.TryLock() {
			c.JSON(409, gin.H{"error": "已有 Suno 歌曲正在导入，请等它完成"})
			return
		}
		defer sunoImportMu.Unlock()
		ctx, cancel := context.WithTimeout(c.Request.Context(), 6*time.Minute)
		defer cancel()
		result, err := sunoImportRunner(ctx, canonical, id, localMusicDownloadDir())
		if err != nil {
			c.JSON(502, gin.H{"error": err.Error()})
			return
		}
		indexSavedLocalMusicFile(&core.DownloadedSong{SavedPath: result.Path}, localMusicDownloadDir())
		c.JSON(200, result)
	})
}

func allowedSunoMediaURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	return u.Host == "cdn1.suno.ai" || u.Host == "cdn2.suno.ai"
}

func readSunoLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("文件超过导入大小限制")
	}
	return data, nil
}

func fetchSunoPage(ctx context.Context, link string) ([]byte, error) {
	const statePath = "data/usesuno-client.json"
	var state sunoClientState
	data, err := os.ReadFile(statePath)
	if err == nil {
		if json.Unmarshal(data, &state) != nil || uuid.Validate(state.BrowserID) != nil {
			return nil, errors.New("本地 UseSuno 客户端记录损坏，请检查配置")
		}
	} else if os.IsNotExist(err) {
		state.BrowserID = uuid.NewString()
	} else {
		return nil, err
	}
	if err := os.MkdirAll("data", 0700); err != nil {
		return nil, err
	}
	// Keep one stable anonymous identity; never rotate it to evade service limits.
	saveState := func() error {
		b, _ := json.Marshal(state)
		f, e := os.CreateTemp("data", ".usesuno-state-*")
		if e != nil {
			return e
		}
		name := f.Name()
		defer os.Remove(name)
		if _, e = f.Write(b); e != nil {
			f.Close()
			return e
		}
		if e = f.Close(); e != nil {
			return e
		}
		return os.Rename(name, statePath)
	}
	if err := saveState(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://usesuno.com/api/proxy?url="+url.QueryEscape(link), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Origin", "https://usesuno.com")
	req.Header.Set("Referer", "https://usesuno.com/tools/downloader/")
	req.Header.Set("X-UseSuno-Protocol", "1")
	req.Header.Set("X-UseSuno-Browser", state.BrowserID)
	if state.Identity != "" {
		req.Header.Set("X-UseSuno-Identity", state.Identity)
	}
	resp, err := sunoImportClient.Do(req)
	if err != nil {
		return nil, errors.New("连接 UseSuno 失败，请稍后重试")
	}
	defer resp.Body.Close()
	if identity := resp.Header.Get("X-UseSuno-Identity"); identity != "" {
		state.Identity = identity
		if err := saveState(); err != nil {
			return nil, err
		}
	}
	switch resp.StatusCode {
	case 200:
	case 429:
		return nil, errors.New("UseSuno 请求过于频繁或今日额度已用完，请稍后重试；未切换身份或使用官方下载额度")
	case 401, 403:
		return nil, errors.New("UseSuno 拒绝了本次请求，请到下载网站查看是否需要验证；未更改歌曲可见性")
	default:
		return nil, fmt.Errorf("UseSuno 暂时不可用（HTTP %d），请稍后重试", resp.StatusCode)
	}
	return readSunoLimited(resp.Body, 8<<20)
}

func downloadSunoMedia(ctx context.Context, raw, path string, limit int64) error {
	if !allowedSunoMediaURL(raw) {
		return errors.New("歌曲没有提供受支持的公开媒体地址")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return err
	}
	resp, err := sunoImportClient.Do(req)
	if err != nil {
		return errors.New("公开媒体下载失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("公开媒体不可下载（HTTP %d）", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return errors.New("媒体文件太大，已停止下载")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > limit {
		return errors.New("媒体文件太大，已停止下载")
	}
	if n < 1024 {
		return errors.New("媒体响应为空或不完整")
	}
	return nil
}

func sunoProbeDuration(ctx context.Context, path string) (float64, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "file,pipe", "-select_streams", "a:0", "-show_entries", "stream=duration", "-of", "json", path)
	out, err := cmd.Output()
	if err != nil {
		return 0, errors.New("媒体文件中没有可读取的音频")
	}
	var probe struct {
		Streams []struct {
			Duration string `json:"duration"`
		} `json:"streams"`
	}
	if json.Unmarshal(out, &probe) != nil || len(probe.Streams) != 1 {
		return 0, errors.New("没有找到完整音轨")
	}
	d, err := strconv.ParseFloat(probe.Streams[0].Duration, 64)
	if err != nil || d <= 0 || math.IsNaN(d) || math.IsInf(d, 0) {
		return 0, errors.New("无法确认音频时长")
	}
	return d, nil
}

func sunoExisting(root, id string) (*sunoImportResult, error) {
	// Our deterministic name makes repeated imports idempotent; manual imports
	// may be registered in the private manifest without changing their filenames.
	relative := "Suno_" + id + ".mp3"
	var manifest map[string]string
	if data, err := os.ReadFile(filepath.Join(root, ".suno-import-index.json")); err == nil && json.Unmarshal(data, &manifest) == nil {
		if p := manifest[id]; p != "" && filepath.Base(p) == p {
			relative = p
		}
	}
	path := filepath.Join(root, relative)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, errors.New("目标文件已存在但无效，未覆盖原文件")
	}
	lyrics, _ := readLocalMusicLyrics(path)
	kind := "none"
	lyricPath := ""
	if lyrics != "" {
		kind = "text"
		if sunoTimedLyrics.MatchString(lyrics) {
			kind = "synced"
		}
	}
	if p, _, ok := localMusicSidecarFile(path, localMusicLyricExts); ok {
		lyricPath = p
	}
	name := strings.TrimSuffix(relative, filepath.Ext(relative))
	if track, err := buildLocalMusicTrackFast(root, path); err == nil {
		name = track.Name
	}
	return &sunoImportResult{ID: id, Name: name, Path: path, LyricPath: lyricPath, Lyrics: lyrics, LyricKind: kind, Existing: true}, nil
}

func importSunoSong(ctx context.Context, link, id, root string) (*sunoImportResult, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	// Never recreate a missing external-volume path on the system disk.
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, errors.New("音乐目录不可用，请确认 Elements2 已连接且下载目录存在")
	}
	if id != "" {
		if existing, err := sunoExisting(root, id); err != nil || existing != nil {
			return existing, err
		}
	}
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			return nil, fmt.Errorf("需要安装 %s 后才能导入音频", tool)
		}
	}
	page, err := fetchSunoPage(ctx, link)
	if err != nil {
		return nil, err
	}
	song, err := parseSunoPage(page, id)
	if err != nil {
		return nil, err
	}
	if existing, err := sunoExisting(root, song.ID); err != nil || existing != nil {
		return existing, err
	}
	// Only media URLs actually present in the public page are candidates. In
	// particular, do not guess a CDN URL when Suno returns /api/forbidden.
	mediaURL := song.AudioURL
	if !allowedSunoMediaURL(mediaURL) {
		mediaURL = song.VideoURL
	}
	if !allowedSunoMediaURL(mediaURL) {
		return nil, errors.New("这首歌没有可下载的公开媒体，请在 UseSuno 网站检查；尚未导入")
	}
	tmp, err := os.MkdirTemp(root, ".suno-import-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	source := filepath.Join(tmp, "source.part")
	if err := downloadSunoMedia(ctx, mediaURL, source, 192<<20); err != nil {
		return nil, err
	}
	duration, err := sunoProbeDuration(ctx, source)
	if err != nil {
		return nil, err
	}
	if math.Abs(duration-song.Metadata.Duration) > math.Max(3, song.Metadata.Duration*0.03) {
		return nil, errors.New("媒体时长与完整歌曲不符，已停止导入")
	}
	output := filepath.Join(tmp, "audio.part")
	lyrics := strings.TrimSpace(song.Metadata.Prompt)
	kind := "none"
	lyricExt := ".txt"
	if lyrics != "" {
		kind = "text"
		if sunoTimedLyrics.MatchString(lyrics) {
			kind = "synced"
			lyricExt = ".lrc"
		}
	}
	args := []string{"-v", "error", "-nostdin", "-n", "-protocol_whitelist", "file,pipe", "-i", source}
	cover := filepath.Join(tmp, "cover.part")
	coverOK := allowedSunoMediaURL(song.ImageURL) && downloadSunoMedia(ctx, song.ImageURL, cover, 5<<20) == nil
	if coverOK {
		args = append(args, "-protocol_whitelist", "file,pipe", "-i", cover)
	}
	args = append(args, "-map", "0:a:0", "-c:a", "libmp3lame", "-b:a", "192k")
	if coverOK {
		args = append(args, "-map", "1:v:0", "-c:v", "mjpeg", "-disposition:v:0", "attached_pic")
	}
	args = append(args, "-id3v2_version", "3", "-metadata", "title="+song.Title, "-metadata", "artist="+song.DisplayName, "-metadata", "album=Suno", "-metadata", "comment="+"https://suno.com/song/"+song.ID, "-metadata", "lyrics="+lyrics, "-f", "mp3", output)
	command := exec.CommandContext(ctx, "ffmpeg", args...)
	command.Env = append(os.Environ(), "TMPDIR="+tmp)
	if err := command.Run(); err != nil {
		return nil, errors.New("音频转换失败或任务已取消，未加入曲库")
	}
	if err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-nostdin", "-i", output, "-f", "null", "-").Run(); err != nil {
		return nil, errors.New("音频完整性检查失败，未加入曲库")
	}
	verified, err := sunoProbeDuration(ctx, output)
	if err != nil || math.Abs(verified-duration) > 1 {
		return nil, errors.New("转换后音频时长异常，未加入曲库")
	}
	if ctx.Err() != nil {
		return nil, errors.New("导入已取消")
	}
	stem := filepath.Join(root, "Suno_"+song.ID)
	final := stem + ".mp3"
	lyricPath := ""
	// Publish sidecar first and audio last. Links refuse overwriting and expose
	// only fully written files. Both paths live on the same volume.
	if lyrics != "" {
		staged := filepath.Join(tmp, "lyrics.part")
		if err := os.WriteFile(staged, []byte(lyrics+"\n"), 0644); err != nil {
			return nil, err
		}
		lyricPath = stem + lyricExt
		if err := os.Link(staged, lyricPath); err != nil {
			return nil, errors.New("歌词目标文件已存在或无法保存，未覆盖原文件")
		}
	}
	if err := os.Link(output, final); err != nil {
		if lyricPath != "" {
			os.Remove(lyricPath)
		}
		return nil, errors.New("音频目标文件已存在或无法保存，未覆盖原文件")
	}
	return &sunoImportResult{ID: song.ID, Name: song.Title, Path: final, LyricPath: lyricPath, Lyrics: lyrics, LyricKind: kind}, nil
}

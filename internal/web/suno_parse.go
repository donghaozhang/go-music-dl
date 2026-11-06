package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var sunoUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var sunoShare = regexp.MustCompile(`^[A-Za-z0-9_-]{4,80}$`)
var sunoFlight = regexp.MustCompile(`self\.__next_f\.push\((\[.*?\])\)`)
var sunoRecord = regexp.MustCompile(`(?m)^([0-9a-fA-F]+):`)
var sunoReference = regexp.MustCompile(`^\$[LSH]?([0-9a-fA-F]+)$`)
var sunoMetaTag = regexp.MustCompile(`(?is)<(?:meta|link)\s+[^>]*>`)
var sunoHTMLAttr = regexp.MustCompile(`(?i)([a-z_:]+)\s*=\s*["']([^"']*)["']`)

type sunoSong struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	DisplayName string `json:"display_name"`
	Handle      string `json:"handle"`
	AudioURL    string `json:"audio_url"`
	VideoURL    string `json:"video_url"`
	ImageURL    string `json:"image_url"`
	Status      string `json:"status"`
	Metadata    struct {
		Prompt   string  `json:"prompt"`
		Duration float64 `json:"duration"`
		Tags     string  `json:"tags"`
	} `json:"metadata"`
}

// Canonicalize only supported song/share links; never forward arbitrary URLs.
func normalizeSunoURL(raw string) (string, string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || (u.Host != "suno.com" && u.Host != "www.suno.com") {
		return "", "", errors.New("请粘贴 https://suno.com/song/… 或 https://suno.com/s/… 歌曲链接")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return "", "", errors.New("仅支持单首 Suno 歌曲链接")
	}
	id := ""
	switch {
	case parts[0] == "song" && sunoUUID.MatchString(parts[1]):
		id = strings.ToLower(parts[1])
		parts[1] = id
	case parts[0] == "s" && sunoShare.MatchString(parts[1]):
	default:
		return "", "", errors.New("歌曲链接格式不正确；暂不支持歌单、个人主页和 Hook")
	}
	return "https://suno.com/" + strings.Join(parts, "/"), id, nil
}

// Decode the public page's Next.js Flight records. T records count UTF-8 bytes,
// so lyrics can span lines and script chunks without truncating Chinese text.
func parseSunoPage(page []byte, wanted string) (sunoSong, error) {
	var song sunoSong
	var payload strings.Builder
	for _, m := range sunoFlight.FindAllSubmatch(page, -1) {
		var entry []json.RawMessage
		if json.Unmarshal(m[1], &entry) != nil || len(entry) != 2 {
			continue
		}
		var kind int
		var text string
		if json.Unmarshal(entry[0], &kind) == nil && kind == 1 && json.Unmarshal(entry[1], &text) == nil {
			payload.WriteString(text)
		}
	}
	raw := payload.String()
	records := map[string]any{}
	textEnd := 0
	for _, m := range sunoRecord.FindAllStringSubmatchIndex(raw, -1) {
		if m[0] < textEnd {
			continue
		}
		key := strings.ToLower(raw[m[2]:m[3]])
		tail := raw[m[1]:]
		if strings.HasPrefix(tail, "T") {
			comma := strings.IndexByte(tail, ',')
			if comma < 2 || comma > 10 {
				continue
			}
			n, err := strconv.ParseUint(tail[1:comma], 16, 32)
			if err == nil && n <= uint64(len(tail)-comma-1) {
				records[key] = tail[comma+1 : comma+1+int(n)]
				textEnd = m[1] + comma + 1 + int(n)
			}
			continue
		}
		if len(tail) > 0 && strings.ContainsRune("IHMLJ", rune(tail[0])) {
			tail = tail[1:]
		}
		var v any
		if json.NewDecoder(strings.NewReader(tail)).Decode(&v) == nil {
			records[key] = v
		}
	}
	// Share links must resolve to a song identity exposed in the returned page.
	if wanted == "" {
		for _, tag := range sunoMetaTag.FindAll(page, -1) {
			attrs := map[string]string{}
			for _, attr := range sunoHTMLAttr.FindAllSubmatch(tag, -1) {
				attrs[strings.ToLower(string(attr[1]))] = html.UnescapeString(string(attr[2]))
			}
			link := ""
			if attrs["property"] == "og:url" {
				link = attrs["content"]
			}
			if attrs["rel"] == "canonical" {
				link = attrs["href"]
			}
			if _, resolved, err := normalizeSunoURL(link); err == nil && resolved != "" {
				if wanted != "" && wanted != resolved {
					return song, errors.New("页面歌曲标识不一致，请使用完整歌曲地址")
				}
				wanted = resolved
			}
		}
		if wanted == "" {
			return song, errors.New("短链接未能解析成歌曲，请复制 Suno 完整歌曲地址")
		}
	}
	var expand func(any, int) any
	expand = func(v any, depth int) any {
		if depth > 35 {
			return nil
		}
		switch x := v.(type) {
		case string:
			if m := sunoReference.FindStringSubmatch(x); m != nil {
				if ref, ok := records[strings.ToLower(m[1])]; ok {
					return expand(ref, depth+1)
				}
			}
		case map[string]any:
			out := make(map[string]any, len(x))
			for k, value := range x {
				out[k] = expand(value, depth+1)
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, value := range x {
				out[i] = expand(value, depth+1)
			}
			return out
		}
		return v
	}
	var find func(any, int) map[string]any
	find = func(v any, depth int) map[string]any {
		if depth > 40 {
			return nil
		}
		switch x := v.(type) {
		case map[string]any:
			if id, ok := x["id"].(string); ok && strings.EqualFold(id, wanted) && x["metadata"] != nil && (x["audio_url"] != nil || x["video_url"] != nil) {
				return x
			}
			for _, value := range x {
				if found := find(value, depth+1); found != nil {
					return found
				}
			}
		case []any:
			for _, value := range x {
				if found := find(value, depth+1); found != nil {
					return found
				}
			}
		}
		return nil
	}
	for _, value := range records {
		if found := find(value, 0); found != nil {
			encoded, _ := json.Marshal(expand(found, 0))
			if json.Unmarshal(encoded, &song) == nil && strings.EqualFold(song.ID, wanted) {
				song.ID = strings.ToLower(song.ID)
				if song.Title == "" {
					song.Title = "Suno " + song.ID[:8]
				}
				if song.DisplayName == "" {
					song.DisplayName = song.Handle
				}
				if song.DisplayName == "" {
					song.DisplayName = "Suno"
				}
				if strings.HasPrefix(song.Metadata.Prompt, "$") {
					return sunoSong{}, errors.New("歌词引用未能解析，请稍后重试")
				}
				if song.Status != "complete" {
					return sunoSong{}, errors.New("歌曲尚未生成完成或当前不可用")
				}
				if song.Metadata.Duration <= 0 || song.Metadata.Duration > 3600 {
					return sunoSong{}, errors.New("歌曲时长缺失或超过一小时")
				}
				return song, nil
			}
		}
	}
	return song, fmt.Errorf("未能读取这首歌的信息；可能未开放访问，或 UseSuno 页面格式已变更")
}

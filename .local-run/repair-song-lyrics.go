package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/guohuiyuan/go-music-dl/core"
	"github.com/guohuiyuan/music-lib/model"
)

func main() {
	core.CM.Load()
	defer core.CloseConfigDB()
	song := &model.Song{ID: "7695832909493667890", Source: "soda", Name: "为利禄为功名", Artist: "宋屿森", Album: "Timeless", Duration: 282}
	raw, err := core.GetLyricFunc("soda")(song)
	if err != nil {
		panic(err)
	}
	lrc := core.StandardLRCForExport(raw)
	timestamps := regexp.MustCompile(`(?m)^\[\d{1,3}:\d{1,2}[.:]\d{1,3}\]`).FindAllString(lrc, -1)
	if len(timestamps) < 2 || strings.Contains(lrc, "纯音乐 / 无歌词") {
		panic("没有取得有效同步歌词，未修改本地文件")
	}
	path, err := filepath.Abs("data/downloads/宋屿森 - 为利禄为功名.lrc")
	if err != nil {
		panic(err)
	}
	if existing, err := os.ReadFile(path); err == nil {
		if !bytes.Equal(existing, []byte(lrc)) {
			panic("已有不同歌词文件，保留原文件")
		}
	} else if !os.IsNotExist(err) {
		panic(err)
	} else {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			panic(err)
		}
		if _, err := file.WriteString(lrc); err != nil {
			file.Close()
			panic(err)
		}
		if err := file.Close(); err != nil {
			panic(err)
		}
	}
	settings := core.GetWebSettings()
	settings.DownloadLyricsToLocal = true
	if err := core.SaveWebSettings(settings); err != nil {
		panic(err)
	}
	report := map[string]any{"track_id": song.ID, "source": song.Source, "lyric_path": path, "timestamp_count": len(timestamps), "bytes": len(lrc), "audio_unchanged": true, "download_lyrics_to_local": true, "checked_at": time.Now().Format(time.RFC3339)}
	data, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(".local-run/lyrics-repair.json", append(data, '\n'), 0600); err != nil {
		panic(err)
	}
	fmt.Println("同步歌词已保存；已开启以后下载时同时保存本地歌词。")
}

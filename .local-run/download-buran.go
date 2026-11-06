package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/guohuiyuan/go-music-dl/core"
)

func main() {
	base, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	os.Setenv("QISHUI_SIGNER_URL", "")
	os.Setenv("QISHUI_SIGNER_TOKEN", "")
	os.Setenv("QISHUI_CLIENT_DIR", "/Volumes/Elements2/Applications/汽水音乐.app/Contents")
	os.Setenv("QISHUI_BDMS_PATH", "/Volumes/Elements2/Applications/汽水音乐.app/Contents/Resources/app.asar.unpacked/bdms.node")
	os.Setenv("QISHUI_IDENTITY_FILE", filepath.Join(base, ".local-run", "soda-identity.json"))
	core.CM.Load()
	defer core.CloseConfigDB()
	report := map[string]any{"checked_at": time.Now().Format(time.RFC3339), "share_url": "https://qishui.douyin.com/s/iX3JwwAA/", "local_signer_available": core.LocalSodaSignerAvailable(), "cookie_present": core.CM.Get("soda") != "", "stage": "parse"}
	write := func() {
		data, _ := json.MarshalIndent(report, "", "  ")
		_ = os.WriteFile(".local-run/buran-download.json", append(data, '\n'), 0600)
	}
	fail := func(err error) {
		report["error"] = err.Error()
		report["success"] = false
		write()
		fmt.Println("测试未完成，详情已写入 .local-run/buran-download.json")
	}
	write()
	song, err := core.GetParseFunc("soda")("https://qishui.douyin.com/s/iX3JwwAA/")
	if err != nil {
		fail(err)
		return
	}
	report["id"], report["name"], report["artist"], report["album"], report["duration_seconds"], report["is_vip"] = song.ID, song.Name, song.Artist, song.Album, song.Duration, song.IsVIP
	report["stage"] = "resolve_audio"
	write()
	info, err := core.NewSodaClient(core.CM.Get("soda")).GetDownloadInfo(song)
	if err != nil {
		fail(err)
		return
	}
	report["preview"], report["stream_duration_seconds"], report["format"], report["bitrate"] = info.Preview, info.Duration, info.Format, info.Bitrate
	report["stage"] = "download"
	write()
	dir := filepath.Join(base, "data", "downloads")
	if info.Preview {
		dir = filepath.Join(dir, "试听片段")
	}
	settings := core.GetWebSettings()
	settings.DownloadLyricsToLocal = true
	if err := core.SaveWebSettings(settings); err != nil {
		fail(err)
		return
	}
	result, err := core.SaveSongToFile(song, dir, true, true)
	if err != nil {
		fail(err)
		return
	}
	report["saved_path"], report["bytes"], report["warning"], report["stage"], report["success"] = result.SavedPath, len(result.Data), result.Warning, "downloaded_pending_validation", true
	report["lyric_path"] = result.LyricPath
	write()
	fmt.Println("音频已保存，待核对时长与解码。")
}

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/guohuiyuan/go-music-dl/core"
)

func main() {
	if err := os.MkdirAll("data", 0700); err != nil {
		panic(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	cookieDB := filepath.Join(home, "Library", "Application Support", "SodaMusic", "Cookies")
	value, err := exec.Command("/usr/bin/sqlite3", "-readonly", cookieDB,
		"SELECT value FROM cookies WHERE host_key IN ('.qishui.com','qishui.com') AND name='sessionid' LIMIT 1;").Output()
	if err != nil {
		panic("无法读取本机汽水音乐登录状态")
	}
	session := strings.TrimSpace(string(value))
	if session == "" {
		panic("汽水音乐客户端尚未登录")
	}
	core.CM.Load()
	core.CM.SetAll(map[string]string{"soda": "sessionid=" + session + ";"})
	core.CM.Save()
	settings := core.GetWebSettings()
	settings.SodaClientDir = "/Volumes/Elements2/Applications/汽水音乐.app/Contents"
	settings.SodaSignerURL = ""
	settings.SodaSignerToken = ""
	settings.DownloadDir, err = filepath.Abs("data/downloads")
	if err != nil {
		panic(err)
	}
	if err := core.SaveWebSettings(settings); err != nil {
		panic(err)
	}
	if err := os.Chmod(core.ConfigDBPath(), 0600); err != nil {
		panic(err)
	}
	if err := core.CloseConfigDB(); err != nil {
		panic(err)
	}
	fmt.Println("已配置本机汽水登录与本机客户端目录；未启用远程签名服务。")
}

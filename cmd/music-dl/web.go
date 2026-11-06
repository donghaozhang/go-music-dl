package main

import (
	"github.com/guohuiyuan/go-music-dl/internal/web"
	"github.com/spf13/cobra"
)

var port string
var noBrowser bool
var desktopMode bool
var basePath string
var listenHost string

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "启动 Web 服务模式",
	Run: func(cmd *cobra.Command, args []string) {
		if desktopMode {
			web.StartDesktop(port)
			return
		}
		web.StartWithOptions(port, web.StartOptions{
			ShouldOpenBrowser: !noBrowser,
			ListenHost:        listenHost,
			BasePath:          basePath,
		})
	},
}

func init() {
	webCmd.Flags().StringVarP(&port, "port", "p", "8080", "服务端口")
	webCmd.Flags().StringVar(&listenHost, "host", "", "监听地址，例如 127.0.0.1")
	webCmd.Flags().StringVar(&basePath, "base-path", web.DefaultRoutePrefix, "Web 端基础路径")
	webCmd.Flags().BoolVar(&noBrowser, "no-browser", false, "不自动打开浏览器")
	webCmd.Flags().BoolVar(&desktopMode, "desktop", false, "桌面内嵌模式")
	_ = webCmd.Flags().MarkHidden("desktop")
	rootCmd.AddCommand(webCmd)
}

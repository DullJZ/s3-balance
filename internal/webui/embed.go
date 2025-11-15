package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var distFS embed.FS

// GetDistFS 获取嵌入的前端静态文件系统
func GetDistFS() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

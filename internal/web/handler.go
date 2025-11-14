package web

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Handler Web管理界面处理器
type Handler struct {
	fileSystem http.FileSystem
}

// NewHandler 创建Web处理器
// distFS 应该是通过 embed.FS 嵌入的 dist 目录
func NewHandler(distFS fs.FS) *Handler {
	return &Handler{
		fileSystem: http.FS(distFS),
	}
}

// ServeHTTP 实现 http.Handler 接口
// 处理单页应用的路由，将所有未找到的路径重定向到 index.html
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 清理路径
	p := r.URL.Path
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	// 尝试打开文件
	f, err := h.fileSystem.Open(path.Clean(p))
	if err != nil {
		// 文件不存在，返回 index.html (用于支持前端路由)
		indexFile, err := h.fileSystem.Open("index.html")
		if err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		defer indexFile.Close()

		// 读取 index.html 内容
		stat, err := indexFile.Stat()
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", stat.ModTime(), indexFile.(io.ReadSeeker))
		return
	}
	defer f.Close()

	// 文件存在，检查是否为目录
	stat, err := f.Stat()
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if stat.IsDir() {
		// 如果是目录，尝试返回 index.html
		indexPath := path.Join(p, "index.html")
		indexFile, err := h.fileSystem.Open(indexPath)
		if err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		defer indexFile.Close()

		indexStat, err := indexFile.Stat()
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", indexStat.ModTime(), indexFile.(io.ReadSeeker))
		return
	}

	// 返回文件内容
	http.ServeContent(w, r, stat.Name(), stat.ModTime(), f.(io.ReadSeeker))
}

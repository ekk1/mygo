package httpserver

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
)

// Static 将本地目录挂载到 URL 前缀，支持 GET/HEAD、index.html、目录列表及 Range。
// 目录必须存在；禁止路径和符号链接越界。目录资源随 Shutdown/Close 释放。
// prefix 必须为不含通配符、查询或片段的绝对路径，可带末尾斜线。
func (s *Server) Static(prefix, dir string, middleware ...Middleware) error {
	if !validPath(prefix) || strings.ContainsAny(prefix, "{}?#% \t") {
		return fmt.Errorf("httpserver: invalid static prefix %q", prefix)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	mount := strings.TrimSuffix(prefix, "/") + "/"
	files := http.FileServer(http.FS(confinedFS{root.FS()}))
	h := chain(Methods("GET")(http.StripPrefix(strings.TrimSuffix(mount, "/"), files)), middleware...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		root.Close()
		return http.ErrServerClosed
	}
	if err := s.register(mount, h); err != nil {
		root.Close()
		return err
	}
	s.roots = append(s.roots, root)
	return nil
}

// 将根目录边界拒绝映射为 403，保留不存在文件的 404。
type confinedFS struct{ fs.FS }

func (f confinedFS) Open(name string) (fs.File, error) {
	file, err := f.FS.Open(name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return file, err
}

package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type root struct {
	dir    string
	entry  string
	prefix string
}

type server struct {
	roots    []*root
	url      string
	depth    int
	theme    string
	skip     []string
	launch   func(string) error
	shutdown func()
	tmpl     *template.Template
	watcher  *fsnotify.Watcher
	rootsMu  sync.RWMutex
	mu       sync.Mutex
	subs     map[chan struct{}]struct{}
}

func newRoot(abs string, isDir bool, prefix string) *root {
	if isDir {
		return &root{dir: abs, prefix: prefix}
	}
	return &root{dir: filepath.Dir(abs), entry: filepath.Base(abs), prefix: prefix}
}

func (rt *root) page(rel string) string {
	if rt.prefix == "" {
		return "/" + rel
	}
	return strings.TrimSuffix("/"+rt.prefix+"/"+rel, "/")
}

func (s *server) routes() http.Handler {
	static, _ := fs.Sub(assetFS, "assets")
	mux := http.NewServeMux()
	mux.Handle("GET /_static/", http.StripPrefix("/_static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /_events", s.serveEvents)
	mux.HandleFunc("GET /_tree", s.serveTree)
	mux.HandleFunc("GET /_edit", s.serveEdit)
	mux.HandleFunc("POST /_add", s.serveAdd)
	mux.HandleFunc("POST /_stop", s.serveStop)
	mux.HandleFunc("GET /", s.serveContent)
	return mux
}

func (s *server) resolve(target string) (*root, string) {
	s.rootsMu.RLock()
	defer s.rootsMu.RUnlock()
	rel := strings.TrimPrefix(path.Clean(target), "/")
	head, rest, _ := strings.Cut(rel, "/")
	for _, rt := range s.roots[1:] {
		if head == rt.prefix {
			return rt, rest
		}
	}
	return s.roots[0], rel
}

func (s *server) serveContent(w http.ResponseWriter, r *http.Request) {
	rt, rel := s.resolve(r.URL.Path)
	if rel == "." || rel == "" {
		if rel = rt.entry; rel == "" {
			rel = indexFile(rt.dir)
		}
	}
	if rel == "" {
		s.renderPage(w, http.StatusOK, filepath.Base(rt.dir), "", "")
		return
	}
	full := filepath.Join(rt.dir, filepath.FromSlash(rel))
	if !isMarkdown(rel) {
		http.ServeFile(w, r, full)
		return
	}
	source, err := os.ReadFile(full)
	if err != nil {
		body := "<h1>404</h1><p>no such file: <code>" + template.HTMLEscapeString(rel) + "</code></p>"
		s.renderPage(w, http.StatusNotFound, "404", "", template.HTML(body))
		return
	}
	s.renderPage(w, http.StatusOK, path.Base(rel), rt.page(rel), render(source))
}

func indexFile(dir string) string {
	for _, name := range []string{"README.md", "readme.md", "index.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return name
		}
	}
	return ""
}

func (s *server) renderPage(w http.ResponseWriter, status int, title, page string, content template.HTML) {
	s.rootsMu.RLock()
	tree := len(s.roots) > 1 || s.roots[0].entry == ""
	s.rootsMu.RUnlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = s.tmpl.Execute(w, map[string]any{
		"Theme": s.theme, "Title": title, "Path": page, "Tree": tree, "Content": content,
	})
}

func (s *server) serveTree(w http.ResponseWriter, r *http.Request) {
	s.rootsMu.RLock()
	roots := slices.Clone(s.roots)
	s.rootsMu.RUnlock()
	nodes := []*node{}
	for _, rt := range roots {
		branch := buildTree(s.rootFiles(rt))
		prefixPaths(branch, rt.prefix)
		if rt.prefix != "" && rt.entry == "" {
			branch = []*node{{Type: "dir", Name: filepath.Base(rt.dir), Path: rt.prefix, Children: branch}}
		}
		nodes = append(nodes, branch...)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"root": filepath.Base(roots[0].dir), "nodes": nodes})
}

func (s *server) rootFiles(rt *root) []string {
	if rt.entry != "" {
		return []string{rt.entry}
	}
	_, files := scan(rt.dir, s.depth, s.skip)
	return files
}

func (s *server) serveEdit(w http.ResponseWriter, r *http.Request) {
	rt, rel := s.resolve(r.URL.Query().Get("path"))
	if !isMarkdown(rel) || strings.HasPrefix(rel, "..") {
		http.Error(w, "not a markdown file", http.StatusBadRequest)
		return
	}
	full := filepath.Join(rt.dir, filepath.FromSlash(rel))
	if _, err := os.Stat(full); err != nil {
		http.Error(w, "no such file", http.StatusNotFound)
		return
	}
	if err := s.launch(full); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) serveAdd(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("path")
	info, err := os.Stat(target)
	if !filepath.IsAbs(target) || err != nil {
		http.Error(w, "no such path", http.StatusBadRequest)
		return
	}
	page := s.addRoot(target, info.IsDir())
	s.broadcast()
	fmt.Fprint(w, s.url+page)
}

func (s *server) addRoot(abs string, isDir bool) string {
	s.rootsMu.Lock()
	defer s.rootsMu.Unlock()
	for _, rt := range s.roots {
		if rel, ok := s.served(rt, abs); ok {
			return rt.page(rel)
		}
	}
	rt := newRoot(abs, isDir, fmt.Sprintf("_r%d", len(s.roots)))
	s.roots = append(s.roots, rt)
	s.addWatches(rt)
	return rt.page(rt.entry)
}

func (s *server) served(rt *root, abs string) (string, bool) {
	if rt.entry != "" {
		return rt.entry, abs == filepath.Join(rt.dir, rt.entry)
	}
	if abs == rt.dir {
		return "", true
	}
	rel, err := filepath.Rel(rt.dir, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	slash := filepath.ToSlash(rel)
	return slash, slices.Contains(s.rootFiles(rt), slash)
}

func (s *server) serveStop(w http.ResponseWriter, r *http.Request) {
	dropInstance(s.url)
	w.WriteHeader(http.StatusNoContent)
	if s.shutdown != nil {
		go s.shutdown()
	}
}

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

const (
	cachedFiles = 64
	cachedBytes = 16 << 20
)

const tokenHeader = "X-Mds-Token"

const contentPolicy = "default-src 'none'; script-src 'nonce-%s'; style-src 'self' 'unsafe-inline'; " +
	"img-src * data:; font-src 'self' data:; connect-src 'self'; form-action 'none'; " +
	"base-uri 'none'; frame-ancestors 'none'"

var assetExt = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".svg", ".ico", ".bmp"}

type root struct {
	dir     string
	entry   string
	prefix  string
	mounted bool
}

type view struct {
	status  int
	title   string
	path    string
	stale   bool
	content template.HTML
	mermaid bool
}

type cached struct {
	source  []byte
	content template.HTML
	mermaid bool
}

type tree struct {
	dirs  []string
	files []string
	fresh bool
}

type server struct {
	roots    []*root
	nextRoot int
	port     string
	depth    int
	skip     []string
	token    string
	launch   func(string) error
	shutdown func()
	tmpl     *template.Template
	watcher  *fsnotify.Watcher
	rootsMu  sync.RWMutex

	cacheMu   sync.Mutex
	cache     map[string]cached
	cacheSize int
	order     []string
	trees     map[string]*tree

	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func newServer(first *root, opts options) *server {
	return &server{
		roots:    []*root{first},
		nextRoot: 1,
		depth:    opts.depth,
		skip:     opts.skip,
		token:    rand.Text(),
		launch:   openInEditor,
		tmpl:     template.Must(template.ParseFS(assetFS, "assets/page.html")),
		cache:    map[string]cached{},
		trees:    map[string]*tree{},
		subs:     map[chan struct{}]struct{}{},
	}
}

func (s *server) origin() string {
	return "http://127.0.0.1:" + s.port
}

func newRoot(abs string, isDir bool, prefix string) *root {
	rt := &root{dir: abs, prefix: prefix, mounted: true}
	if !isDir {
		rt.dir, rt.entry = filepath.Dir(abs), filepath.Base(abs)
	}
	return rt
}

func shortPath(dir string) string {
	home, err := os.UserHomeDir()
	if err == nil && (dir == home || strings.HasPrefix(dir, home+string(os.PathSeparator))) {
		dir = "~" + strings.TrimPrefix(dir, home)
	}
	return filepath.ToSlash(dir)
}

func (rt *root) page(rel string) string {
	if rt.prefix == "" {
		return "/" + rel
	}
	return strings.TrimSuffix("/"+rt.prefix+"/"+rel, "/")
}

func (rt *root) covers(file string) bool {
	if rt.entry != "" {
		return file == filepath.Join(rt.dir, rt.entry)
	}
	return strings.HasPrefix(file, rt.dir+string(os.PathSeparator))
}

func (s *server) routes() http.Handler {
	static, _ := fs.Sub(assetFS, "assets")
	mux := http.NewServeMux()
	mux.Handle("GET /_static/", http.StripPrefix("/_static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /_events", s.serveEvents)
	mux.HandleFunc("GET /_tree", s.serveTree)
	mux.HandleFunc("POST /_edit", s.guard(s.serveEdit))
	mux.HandleFunc("POST /_add", s.guard(s.serveAdd))
	mux.HandleFunc("POST /_drop", s.guard(s.serveDrop))
	mux.HandleFunc("POST /_stop", s.guard(s.serveStop))
	mux.HandleFunc("GET /", s.serveContent)
	return s.local(mux)
}

func (s *server) local(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopback(r.Host) {
			http.Error(w, "mds only serves loopback hosts", http.StatusForbidden)
			return
		}
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		next.ServeHTTP(w, r)
	})
}

func (s *server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(tokenHeader) != s.token {
			http.Error(w, "bad or missing "+tokenHeader, http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func loopback(host string) bool {
	name, _, err := net.SplitHostPort(host)
	if err != nil {
		name = host
	}
	if name == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(name, "[]"))
	return ip != nil && ip.IsLoopback()
}

func isAsset(p string) bool {
	return slices.Contains(assetExt, strings.ToLower(filepath.Ext(p)))
}

func readInside(dir, rel string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return fs.ReadFile(root.FS(), rel)
}

func statInside(dir, rel string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	_, err = fs.Stat(root.FS(), rel)
	return err
}

func (s *server) resolve(target string) (*root, string) {
	s.rootsMu.RLock()
	defer s.rootsMu.RUnlock()
	rel := strings.TrimPrefix(path.Clean(target), "/")
	head, rest, _ := strings.Cut(rel, "/")
	for _, rt := range s.roots {
		if rt.prefix != "" && head == rt.prefix {
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
		s.renderPage(w, view{status: http.StatusOK, title: filepath.Base(rt.dir)})
		return
	}
	full := filepath.Join(rt.dir, filepath.FromSlash(rel))
	if !isMarkdown(rel) {
		s.serveAsset(w, r, rt.dir, rel)
		return
	}
	page := view{status: http.StatusOK, title: path.Base(rel), path: rt.page(rel)}
	source, err := readInside(rt.dir, rel)
	if err == nil {
		entry := s.renderFile(full, source)
		page.content, page.mermaid = entry.content, entry.mermaid
		s.renderPage(w, page)
		return
	}
	if entry, ok := s.recall(full); ok {
		page.stale, page.content, page.mermaid = true, entry.content, entry.mermaid
		s.renderPage(w, page)
		return
	}
	s.notFound(w, rel)
}

func (s *server) serveAsset(w http.ResponseWriter, r *http.Request, dir, rel string) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		s.notFound(w, rel)
		return
	}
	defer root.Close()
	if _, err := fs.Stat(root.FS(), rel); err != nil || !isAsset(rel) {
		s.notFound(w, rel)
		return
	}
	http.ServeFileFS(w, r, root.FS(), rel)
}

func (s *server) notFound(w http.ResponseWriter, rel string) {
	body := "<h1>404</h1><p>no such file: <code>" + template.HTMLEscapeString(rel) + "</code></p>"
	s.renderPage(w, view{status: http.StatusNotFound, title: "404", content: template.HTML(body)})
}

func indexFile(dir string) string {
	for _, name := range []string{"README.md", "readme.md", "index.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return name
		}
	}
	return ""
}

func (s *server) renderPage(w http.ResponseWriter, v view) {
	s.rootsMu.RLock()
	tree := len(s.roots) > 1 || s.roots[0].entry == ""
	s.rootsMu.RUnlock()
	nonce := rand.Text()
	w.Header().Set("Content-Security-Policy", fmt.Sprintf(contentPolicy, nonce))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(v.status)
	_ = s.tmpl.Execute(w, map[string]any{
		"Title": v.title, "Path": v.path, "Tree": tree,
		"Stale": v.stale, "Content": v.content, "Mermaid": v.mermaid,
		"Nonce": nonce, "Token": s.token,
	})
}

func (entry cached) size() int {
	return len(entry.source) + len(entry.content)
}

func (s *server) remember(file string, entry cached) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if old, ok := s.cache[file]; ok {
		s.cacheSize -= old.size()
	} else {
		s.order = append(s.order, file)
	}
	s.cache[file] = entry
	s.cacheSize += entry.size()
	for len(s.order) > 1 && (len(s.order) > cachedFiles || s.cacheSize > cachedBytes) {
		oldest := s.order[0]
		s.order = s.order[1:]
		s.cacheSize -= s.cache[oldest].size()
		delete(s.cache, oldest)
	}
}

func (s *server) recall(file string) (cached, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	entry, ok := s.cache[file]
	return entry, ok
}

func (s *server) renderFile(file string, source []byte) cached {
	if entry, ok := s.recall(file); ok && bytes.Equal(entry.source, source) {
		return entry
	}
	content, mermaid := render(source)
	entry := cached{source: source, content: content, mermaid: mermaid}
	s.remember(file, entry)
	return entry
}

func (s *server) forget(rt *root) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	delete(s.trees, rt.prefix)
	for _, file := range s.order {
		if rt.covers(file) {
			s.cacheSize -= s.cache[file].size()
			delete(s.cache, file)
		}
	}
	s.order = slices.DeleteFunc(s.order, rt.covers)
}

func (s *server) serveTree(w http.ResponseWriter, r *http.Request) {
	s.rootsMu.RLock()
	roots := slices.Clone(s.roots)
	s.rootsMu.RUnlock()
	nodes := []*node{}
	for _, rt := range roots {
		branch := buildTree(s.rootFiles(rt))
		prefixPaths(branch, rt.prefix)
		if len(roots) > 1 {
			branch = []*node{{Type: "root", Name: shortPath(rt.dir), Path: rt.prefix, Children: branch}}
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
	return s.rootTree(rt).files
}

func (s *server) rootTree(rt *root) *tree {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	known := s.trees[rt.prefix]
	if known != nil && known.fresh {
		return known
	}
	dirs, files := scan(rt.dir, s.depth, s.skip)
	if len(files) == 0 && known != nil {
		if _, err := os.Stat(rt.dir); err != nil {
			return known
		}
	}
	found := &tree{dirs: dirs, files: files, fresh: true}
	s.trees[rt.prefix] = found
	return found
}

func (s *server) rescan() {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	for _, known := range s.trees {
		known.fresh = false
	}
}

func (s *server) serveEdit(w http.ResponseWriter, r *http.Request) {
	rt, rel := s.resolve(r.URL.Query().Get("path"))
	if !isMarkdown(rel) || strings.HasPrefix(rel, "..") {
		http.Error(w, "not a markdown file", http.StatusBadRequest)
		return
	}
	if err := statInside(rt.dir, rel); err != nil {
		http.Error(w, "no such file", http.StatusNotFound)
		return
	}
	if err := s.launch(filepath.Join(rt.dir, filepath.FromSlash(rel))); err != nil {
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
	fmt.Fprint(w, s.origin()+page)
}

func (s *server) addRoot(abs string, isDir bool) string {
	s.rootsMu.Lock()
	defer s.rootsMu.Unlock()
	for _, rt := range s.roots {
		if rel, ok := s.served(rt, abs); ok {
			return rt.page(rel)
		}
	}
	rt := newRoot(abs, isDir, fmt.Sprintf("_r%d", s.nextRoot))
	s.nextRoot++
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

func (s *server) serveDrop(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if !query.Has("root") || !s.dropRoot(query.Get("root")) {
		http.Error(w, "no such root, or it is the last one", http.StatusBadRequest)
		return
	}
	s.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) dropRoot(prefix string) bool {
	s.rootsMu.Lock()
	defer s.rootsMu.Unlock()
	index := slices.IndexFunc(s.roots, func(rt *root) bool { return rt.prefix == prefix })
	if index < 0 || len(s.roots) < 2 {
		return false
	}
	s.forget(s.roots[index])
	s.roots = slices.Delete(s.roots, index, index+1)
	return true
}

func (s *server) serveStop(w http.ResponseWriter, r *http.Request) {
	dropInstance(s.port)
	w.WriteHeader(http.StatusNoContent)
	_ = http.NewResponseController(w).Flush()
	if s.shutdown != nil {
		go s.shutdown()
	}
}

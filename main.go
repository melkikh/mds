package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

//go:embed assets
var assetFS embed.FS

const usage = `mds — render markdown in the browser

usage:
  mds [path] [flags]

  path               file or directory to serve (default ".")
  -d, --depth N      max directory depth, 0 = root only, -1 = unlimited (default 5)
  -t, --theme NAME   initial theme, light or dark (default "light")
  -s, --skip NAMES   comma-separated directory names to skip (default "node_modules,vendor,dist,build,target")
  -b, --background   serve in a detached process, print the URL and exit
      --new          start a separate server instead of reusing the running one
      --stop         stop the running server
      --skill        print a skill file teaching a coding agent to use mds
  -h, --help         show this help

  MDS_EDITOR         command the pencil button runs, e.g. "code -g" (default: system opener)

A second mds adds its path to the server that is already running and prints the URL of
that page; the sidebar of the open tab picks it up. Running under a coding agent
(Claude Code, Codex, ...) implies --background.
`

var defaultSkip = []string{"node_modules", "vendor", "dist", "build", "target"}

type options struct {
	target     string
	depth      int
	theme      string
	skip       []string
	background bool
	fresh      bool
}

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

func main() {
	opts := parseArgs(os.Args[1:])
	abs, err := filepath.Abs(opts.target)
	if err != nil {
		fatal(err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		fatal(err)
	}
	agent := detectAgent()
	if !opts.fresh {
		if page, ok := addToRunning(abs); ok {
			fmt.Println(page)
			if agent != "" {
				fmt.Print(reuseHint)
			}
			return
		}
	}
	if os.Getenv(childEnv) == "" && (opts.background || agent != "") {
		detach(agent)
		return
	}
	s := &server{
		roots:  []*root{newRoot(abs, info.IsDir(), "")},
		depth:  opts.depth,
		theme:  opts.theme,
		skip:   opts.skip,
		launch: openInEditor,
		tmpl:   template.Must(template.ParseFS(assetFS, "assets/page.html")),
		subs:   map[chan struct{}]struct{}{},
	}
	listener, serverURL := listen()
	httpServer := &http.Server{Handler: s.routes()}
	s.url, s.shutdown = serverURL, func() { _ = httpServer.Close() }
	fmt.Println(serverURL)
	_ = openExternal(serverURL)
	addInstance(serverURL)
	go s.watch()
	if err := httpServer.Serve(listener); err != http.ErrServerClosed {
		fatal(err)
	}
	dropInstance(serverURL)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "mds:", err)
	os.Exit(1)
}

func parseArgs(args []string) options {
	opts := options{target: ".", depth: 5, theme: "light", skip: defaultSkip}
	for i := 0; i < len(args); i++ {
		value := ""
		if i+1 < len(args) {
			value = args[i+1]
		}
		switch args[i] {
		case "-h", "--help":
			fmt.Print(usage)
			os.Exit(0)
		case "--skill":
			printSkill()
			os.Exit(0)
		case "--stop":
			stopRunning()
			os.Exit(0)
		case "-b", "--background":
			opts.background = true
		case "--new":
			opts.fresh = true
		case "-d", "--depth":
			if n, err := strconv.Atoi(value); err == nil {
				opts.depth = n
			}
			i++
		case "-t", "--theme":
			if value == "dark" {
				opts.theme = "dark"
			}
			i++
		case "-s", "--skip":
			opts.skip = strings.Split(value, ",")
			i++
		default:
			opts.target = args[i]
		}
	}
	return opts
}

func listen() (net.Listener, string) {
	if l, err := net.Listen("tcp", "127.0.0.1:8080"); err == nil {
		return l, "http://127.0.0.1:8080"
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal(err)
	}
	return l, fmt.Sprintf("http://127.0.0.1:%d", l.Addr().(*net.TCPAddr).Port)
}

func openExternal(target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}

func openInEditor(target string) error {
	editor := strings.Fields(os.Getenv("MDS_EDITOR"))
	if len(editor) == 0 {
		return openExternal(target)
	}
	return exec.Command(editor[0], append(editor[1:], target)...).Start()
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

func (s *server) serveEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	stream := http.NewResponseController(w)
	_ = stream.Flush()
	updates := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[updates] = struct{}{}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.subs, updates); s.mu.Unlock() }()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-updates:
			fmt.Fprint(w, "data: reload\n\n")
			_ = stream.Flush()
		}
	}
}

func (s *server) broadcast() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for updates := range s.subs {
		select {
		case updates <- struct{}{}:
		default:
		}
	}
}

func (s *server) watch() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	defer watcher.Close()
	s.rootsMu.Lock()
	s.watcher = watcher
	s.watchRoots()
	s.rootsMu.Unlock()
	debounce := time.AfterFunc(time.Hour, s.broadcast)
	debounce.Stop()
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
				s.rootsMu.RLock()
				s.watchRoots()
				s.rootsMu.RUnlock()
			} else if !s.watched(event.Name) {
				continue
			}
			debounce.Reset(100 * time.Millisecond)
		case <-watcher.Errors:
		}
	}
}

func (s *server) watchRoots() {
	for _, rt := range s.roots {
		s.addWatches(rt)
	}
}

func (s *server) addWatches(rt *root) {
	if s.watcher == nil {
		return
	}
	if rt.entry != "" {
		_ = s.watcher.Add(rt.dir)
		return
	}
	dirs, _ := scan(rt.dir, s.depth, s.skip)
	for _, dir := range dirs {
		_ = s.watcher.Add(dir)
	}
}

func (s *server) watched(name string) bool {
	if !isMarkdown(name) {
		return false
	}
	s.rootsMu.RLock()
	defer s.rootsMu.RUnlock()
	for _, rt := range s.roots {
		if rt.entry == "" {
			if strings.HasPrefix(name, rt.dir+string(os.PathSeparator)) {
				return true
			}
		} else if name == filepath.Join(rt.dir, rt.entry) {
			return true
		}
	}
	return false
}

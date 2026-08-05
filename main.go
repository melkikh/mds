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
  -h, --help         show this help

  MDS_EDITOR         command the pencil button runs, e.g. "code -g" (default: system opener)
`

var defaultSkip = []string{"node_modules", "vendor", "dist", "build", "target"}

type server struct {
	root    string
	entry   string
	dirMode bool
	depth   int
	theme   string
	skip    []string
	launch  func(string) error
	tmpl    *template.Template
	mu      sync.Mutex
	subs    map[chan struct{}]struct{}
}

func main() {
	target, depth, theme, skip := parseArgs(os.Args[1:])
	abs, err := filepath.Abs(target)
	if err != nil {
		fatal(err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		fatal(err)
	}
	s := &server{
		root:   abs,
		depth:  depth,
		theme:  theme,
		skip:   skip,
		launch: openInEditor,
		tmpl:   template.Must(template.ParseFS(assetFS, "assets/page.html")),
		subs:   map[chan struct{}]struct{}{},
	}
	if s.dirMode = info.IsDir(); !s.dirMode {
		s.root, s.entry = filepath.Dir(abs), filepath.Base(abs)
	}
	listener, url := listen()
	fmt.Println(url)
	_ = openExternal(url)
	go s.watch()
	fatal(http.Serve(listener, s.routes()))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "mds:", err)
	os.Exit(1)
}

func parseArgs(args []string) (string, int, string, []string) {
	target, depth, theme, skip := ".", 5, "light", defaultSkip
	for i := 0; i < len(args); i++ {
		value := ""
		if i+1 < len(args) {
			value = args[i+1]
		}
		switch args[i] {
		case "-h", "--help":
			fmt.Print(usage)
			os.Exit(0)
		case "-d", "--depth":
			if n, err := strconv.Atoi(value); err == nil {
				depth = n
			}
			i++
		case "-t", "--theme":
			if value == "dark" {
				theme = "dark"
			}
			i++
		case "-s", "--skip":
			skip = strings.Split(value, ",")
			i++
		default:
			target = args[i]
		}
	}
	return target, depth, theme, skip
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

func (s *server) routes() http.Handler {
	static, _ := fs.Sub(assetFS, "assets")
	mux := http.NewServeMux()
	mux.Handle("GET /_static/", http.StripPrefix("/_static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /_events", s.serveEvents)
	mux.HandleFunc("GET /_tree", s.serveTree)
	mux.HandleFunc("GET /_edit", s.serveEdit)
	mux.HandleFunc("GET /", s.serveContent)
	return mux
}

func (s *server) serveContent(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if rel == "." || rel == "" {
		if rel = s.entry; s.dirMode {
			rel = s.indexFile()
		}
	}
	if rel == "" {
		s.renderPage(w, http.StatusOK, filepath.Base(s.root), "", "")
		return
	}
	full := filepath.Join(s.root, filepath.FromSlash(rel))
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
	s.renderPage(w, http.StatusOK, path.Base(rel), rel, render(source))
}

func (s *server) indexFile() string {
	for _, name := range []string{"README.md", "readme.md", "index.md"} {
		if _, err := os.Stat(filepath.Join(s.root, name)); err == nil {
			return name
		}
	}
	return ""
}

func (s *server) renderPage(w http.ResponseWriter, status int, title, rel string, content template.HTML) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = s.tmpl.Execute(w, map[string]any{
		"Theme": s.theme, "Title": title, "Path": rel, "DirMode": s.dirMode, "Content": content,
	})
}

func (s *server) serveTree(w http.ResponseWriter, r *http.Request) {
	_, files := scan(s.root, s.depth, s.skip)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"root": filepath.Base(s.root), "nodes": buildTree(files)})
}

func (s *server) serveEdit(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(path.Clean(r.URL.Query().Get("path")), "/")
	if !isMarkdown(rel) || strings.HasPrefix(rel, "..") {
		http.Error(w, "not a markdown file", http.StatusBadRequest)
		return
	}
	full := filepath.Join(s.root, filepath.FromSlash(rel))
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
	s.addWatches(watcher)
	debounce := time.AfterFunc(time.Hour, s.broadcast)
	debounce.Stop()
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if info, err := os.Stat(event.Name); s.dirMode && err == nil && info.IsDir() {
				s.addWatches(watcher)
			} else if !s.watched(event.Name) {
				continue
			}
			debounce.Reset(100 * time.Millisecond)
		case <-watcher.Errors:
		}
	}
}

func (s *server) watched(name string) bool {
	if s.dirMode {
		return isMarkdown(name)
	}
	return name == filepath.Join(s.root, s.entry)
}

func (s *server) addWatches(watcher *fsnotify.Watcher) {
	if !s.dirMode {
		_ = watcher.Add(s.root)
		return
	}
	dirs, _ := scan(s.root, s.depth, s.skip)
	for _, dir := range dirs {
		_ = watcher.Add(dir)
	}
}

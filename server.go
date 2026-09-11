package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/fsnotify/fsnotify"
)

const (
	cachedFiles = 64
	cachedBytes = 16 << 20
)

const tokenHeader = "X-Mds-Token"

const tabsHeader = "X-Mds-Tabs"

const (
	// sessionCookie holds the key once a page has traded the fragment for it, tokenParam
	// names that fragment field, maxKey caps what /_auth will even look at.
	sessionCookie = "mds_key"
	tokenParam    = "key"
	maxKey        = 256
)

const contentPolicy = "default-src 'none'; script-src 'nonce-%s'; style-src 'self' 'unsafe-inline'; " +
	"img-src %s; font-src 'self' data:; connect-src 'self'; form-action 'none'; " +
	"base-uri 'none'; frame-ancestors 'none'"

const remoteEnv = "MDS_REMOTE_IMAGES"

// imagePolicy decides whether a document may reach off this machine for pictures. It may
// not by default: a remote <img> in a file someone else wrote is both a "he opened it"
// beacon and, with inline css allowed, a way to spell out what is on the page one request
// at a time. MDS_REMOTE_IMAGES=1 gets badges and other remote art back; anything ParseBool
// reads as false, and anything it cannot read at all, leaves them blocked.
func imagePolicy() string {
	if remote, _ := strconv.ParseBool(os.Getenv(remoteEnv)); remote {
		return "* data:"
	}
	return "'self' data:"
}

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
	flavor  flavor
	stale   bool
	content template.HTML
	mermaid bool
}

type cached struct {
	source  []byte
	flavor  flavor
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
	assigned map[string]string
	port     string
	depth    int
	skip     []string
	token    string
	action   string
	launch   func(string) error
	shutdown func()
	tmpl     *template.Template
	watcher  *fsnotify.Watcher
	rootsMu  sync.RWMutex

	sessionMu   sync.RWMutex
	session     string
	sessions    []sessionHash
	sessionFile string

	cacheMu   sync.Mutex
	cache     map[string]cached
	cacheSize int
	order     []string
	trees     map[string]*tree

	mu   sync.Mutex
	subs map[chan string]struct{}
}

// newServer takes the root mds was started on, or nil: the login server opens with nothing
// mounted and waits for the first mds <path> to hand it one.
func newServer(first *root, opts options) *server {
	s := &server{
		roots:    []*root{},
		assigned: map[string]string{},
		depth:    opts.depth,
		skip:     opts.skip,
		token:    rand.Text(),
		action:   rand.Text(),
		launch:   openInEditor,
		tmpl:     template.Must(template.ParseFS(assetFS, "assets/page.html", "assets/gate.html")),
		cache:    map[string]cached{},
		trees:    map[string]*tree{},
		subs:     map[chan string]struct{}{},
	}
	if opts.service == serviceRun {
		s.sessionFile = serviceSessionsFile()
		s.sessions = readSessionHashes(s.sessionFile)
	} else {
		s.session = rand.Text()
		s.sessions = []sessionHash{hashSession(s.session)}
	}
	if first != nil {
		first.prefix = s.prefix(first.dir)
		s.roots = append(s.roots, first)
	}
	return s
}

// home is where a bare / lands: the first root's own page, or the empty page a server with
// nothing mounted shows instead.
func (s *server) home() string {
	s.rootsMu.RLock()
	defer s.rootsMu.RUnlock()
	if len(s.roots) == 0 {
		return "/"
	}
	return s.roots[0].page(s.roots[0].entry)
}

// link is what mds prints and what the browser is sent to, key and all.
func (s *server) link(page string) string {
	return s.origin() + page + "#" + tokenParam + "=" + s.token
}

func (s *server) origin() string {
	return "http://127.0.0.1:" + s.port
}

func newRoot(abs string, isDir bool) *root {
	rt := &root{dir: abs, mounted: true}
	if !isDir {
		rt.dir, rt.entry = filepath.Dir(abs), filepath.Base(abs)
	}
	return rt
}

// prefix names a root after its own directory, so a url reads /notes/todo.md rather than
// /_r2/todo.md. Every root has one, including the first: one shape for every page.
// A name a second directory would want is handed out once and kept, so a link never
// quietly changes which root it points at.
func (s *server) prefix(dir string) string {
	name := prefixName(dir)
	for suffix := 1; ; suffix++ {
		taken := name
		if suffix > 1 {
			taken = fmt.Sprintf("%s-%d", name, suffix)
		}
		if owner, ok := s.assigned[taken]; !ok || owner == dir {
			s.assigned[taken] = dir
			return taken
		}
	}
}

func prefixName(dir string) string {
	clean := strings.Map(func(r rune) rune {
		if r == '.' || r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return '-'
	}, filepath.Base(dir))
	// a leading _ is the shape of mds's own routes, a leading . or - reads like a flag
	clean = strings.TrimLeft(clean, "_.-")
	if clean == "" {
		return "files"
	}
	return clean
}

func shortPath(dir string) string {
	home, err := os.UserHomeDir()
	if err == nil && (dir == home || strings.HasPrefix(dir, home+string(os.PathSeparator))) {
		dir = "~" + strings.TrimPrefix(dir, home)
	}
	return filepath.ToSlash(dir)
}

// target is the path mds was given for this root, directory or single file.
func (rt *root) target() string {
	return filepath.Join(rt.dir, rt.entry)
}

func (rt *root) page(rel string) string {
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
	mux.HandleFunc("GET /_raw", s.serveRaw)
	mux.HandleFunc("POST /_edit", s.guard(s.serveEdit))
	mux.HandleFunc("POST /_add", s.mine(s.serveAdd))
	mux.HandleFunc("POST /_drop", s.guard(s.serveDrop))
	mux.HandleFunc("POST /_stop", s.guard(s.serveStop))
	mux.HandleFunc("GET /", s.serveContent)

	front := http.NewServeMux()
	front.HandleFunc("POST /_auth", s.serveAuth)
	front.Handle("/", s.authenticate(mux))
	return s.local(front)
}

func (s *server) local(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopback(r.Host) {
			http.Error(w, "mds only serves loopback hosts", http.StatusForbidden)
			return
		}
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		next.ServeHTTP(w, r)
	})
}

// authenticate keeps everything behind the key mds printed. The browser gets in by trading
// the key in the url fragment for a cookie, so the key itself never travels to the server
// in a url anything else could log or remember.
func (s *server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authorized(r) {
			next.ServeHTTP(w, r)
			return
		}
		s.serveGate(w)
	})
}

// Three secrets, so that losing one is not losing everything. token is the master key: it
// travels in the fragment and lives in the instances file, and only mds itself carries it.
// session is what the cookie holds — cookies are not scoped by port, so anything else
// listening on localhost may end up seeing it, and on its own it is read-only. action sits
// in the page markup, where a stylesheet in a hostile document could in principle spell it
// out; it does nothing without the cookie beside it.
func (s *server) authorized(r *http.Request) bool {
	return s.hasSession(r) || s.hasMaster(r)
}

func (s *server) hasSession(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	return err == nil && s.acceptsSession(cookie.Value)
}

func (s *server) hasMaster(r *http.Request) bool {
	return sameToken(r.Header.Get(tokenHeader), s.token)
}

func sameToken(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *server) serveAuth(w http.ResponseWriter, r *http.Request) {
	key, err := io.ReadAll(io.LimitReader(r.Body, maxKey))
	if err != nil || !sameToken(strings.TrimSpace(string(key)), s.token) {
		http.Error(w, "that key is not this server's", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.issueSession(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) serveGate(w http.ResponseWriter) {
	nonce := rand.Text()
	w.Header().Set("Content-Security-Policy", fmt.Sprintf(contentPolicy, nonce, imagePolicy()))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_ = s.tmpl.ExecuteTemplate(w, "gate.html", map[string]any{"Nonce": nonce, "Param": tokenParam})
}

// guard lets the open page act — with its cookie and the value its own markup carries,
// both — and lets mds itself act with the master key.
func (s *server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acting := s.hasSession(r) && sameToken(r.Header.Get(tokenHeader), s.action)
		if !acting && !s.hasMaster(r) {
			http.Error(w, "bad or missing "+tokenHeader, http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// mounting a new root reaches anywhere on the disk, so only mds itself may ask for it.
func (s *server) mine(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.hasMaster(r) {
			http.Error(w, "only mds itself can mount a path", http.StatusForbidden)
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

// resolve splits /prefix/rel into the root that owns it and the path inside that root.
// Nothing lives outside a root, so an unknown prefix resolves to nothing at all.
func (s *server) resolve(target string) (*root, string) {
	s.rootsMu.RLock()
	defer s.rootsMu.RUnlock()
	head, rest, _ := strings.Cut(strings.TrimPrefix(path.Clean(target), "/"), "/")
	for _, rt := range s.roots {
		if head == rt.prefix {
			return rt, rest
		}
	}
	return nil, ""
}

// waiting is what a server with nothing mounted has to show for itself.
const waiting = template.HTML(`<h1>mds</h1><p>nothing is open yet. ` +
	`run <code>mds &lt;path&gt;</code> and it lands here.</p>`)

func (s *server) serveContent(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		if page := s.home(); page != "/" {
			http.Redirect(w, r, page, http.StatusFound)
			return
		}
		s.renderPage(w, view{status: http.StatusOK, title: "mds", content: waiting})
		return
	}
	rt, rel := s.resolve(r.URL.Path)
	if rt == nil {
		s.notFound(w, r.URL.Path)
		return
	}
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
	selected, forced := requestFlavor(r)
	source, err := readInside(rt.dir, rel)
	if err == nil {
		if !forced && detectYFM(source) {
			selected = yfmFlavor
		}
		entry := s.renderFile(full, source, selected)
		page.flavor = selected
		page.content, page.mermaid = entry.content, entry.mermaid
		s.renderPage(w, page)
		return
	}
	entry, ok := s.recall(full)
	if forced {
		entry, ok = s.recallFlavor(full, selected)
	}
	if ok {
		page.flavor = entry.flavor
		if page.flavor == "" {
			page.flavor = markdownFlavor
		}
		page.stale, page.content, page.mermaid = true, entry.content, entry.mermaid
		s.renderPage(w, page)
		return
	}
	s.notFound(w, rel)
}

func (s *server) serveRaw(w http.ResponseWriter, r *http.Request) {
	rt, rel := s.resolve(r.URL.Query().Get("path"))
	if rt == nil || !isMarkdown(rel) || strings.HasPrefix(rel, "..") {
		http.Error(w, "not a markdown file", http.StatusBadRequest)
		return
	}
	source, err := readInside(rt.dir, rel)
	if err != nil {
		entry, ok := s.recall(filepath.Join(rt.dir, filepath.FromSlash(rel)))
		if !ok {
			http.Error(w, "no such file", http.StatusNotFound)
			return
		}
		source = entry.source
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(source)
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
	tree := len(s.roots) > 1 || (len(s.roots) == 1 && s.roots[0].entry == "")
	s.rootsMu.RUnlock()
	nonce := rand.Text()
	w.Header().Set("Content-Security-Policy", fmt.Sprintf(contentPolicy, nonce, imagePolicy()))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(v.status)
	_ = s.tmpl.ExecuteTemplate(w, "page.html", map[string]any{
		"Title": v.title, "Path": v.path, "Tree": tree,
		"Stale": v.stale, "Content": v.content, "Mermaid": v.mermaid, "Flavor": v.flavor,
		"Nonce": nonce, "Token": s.action,
	})
}

func requestFlavor(r *http.Request) (flavor, bool) {
	switch r.URL.Query().Get("flavor") {
	case "yfm":
		return yfmFlavor, true
	case "md":
		return markdownFlavor, true
	default:
		return markdownFlavor, false
	}
}

func (entry cached) size() int {
	return len(entry.source) + len(entry.content)
}

func (s *server) remember(file string, entry cached) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	key := flavorCacheKey(file, entry.flavor)
	if old, ok := s.cache[key]; ok {
		s.cacheSize -= old.size()
	} else {
		s.order = append(s.order, key)
	}
	s.cache[key] = entry
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
	for i := len(s.order) - 1; i >= 0; i-- {
		if cacheFile(s.order[i]) == file {
			return s.cache[s.order[i]], true
		}
	}
	return cached{}, false
}

func (s *server) recallFlavor(file string, flavor flavor) (cached, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	entry, ok := s.cache[flavorCacheKey(file, flavor)]
	return entry, ok
}

func flavorCacheKey(file string, flavor flavor) string {
	if flavor == yfmFlavor {
		return file + "\x00yfm"
	}
	return file
}

func cacheFile(key string) string {
	file, _, _ := strings.Cut(key, "\x00")
	return file
}

func (s *server) renderFile(file string, source []byte, flavor flavor) cached {
	if entry, ok := s.recallFlavor(file, flavor); ok && bytes.Equal(entry.source, source) {
		return entry
	}
	content, mermaid := renderFlavor(source, flavor)
	entry := cached{source: source, flavor: flavor, content: content, mermaid: mermaid}
	s.remember(file, entry)
	return entry
}

func (s *server) forget(rt *root) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	delete(s.trees, rt.prefix)
	for _, key := range s.order {
		if rt.covers(cacheFile(key)) {
			s.cacheSize -= s.cache[key].size()
			delete(s.cache, key)
		}
	}
	s.order = slices.DeleteFunc(s.order, func(key string) bool { return rt.covers(cacheFile(key)) })
}

func (s *server) serveTree(w http.ResponseWriter, r *http.Request) {
	s.rootsMu.RLock()
	roots := slices.Clone(s.roots)
	s.rootsMu.RUnlock()
	nodes := []*node{}
	for _, rt := range roots {
		branch := buildTree(s.rootFiles(rt))
		prefixPaths(branch, rt.prefix)
		nodes = append(nodes, &node{Type: "root", Name: shortPath(rt.dir), Path: rt.prefix, Children: branch})
	}
	name := ""
	if len(roots) > 0 {
		name = filepath.Base(roots[0].dir)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"root": name, "nodes": nodes})
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
	if rt == nil || !isMarkdown(rel) || strings.HasPrefix(rel, "..") {
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
	w.Header().Set(tabsHeader, strconv.Itoa(s.tabs()))
	s.broadcast("go " + page)
	fmt.Fprint(w, s.link(page))
}

func (s *server) addRoot(abs string, isDir bool) string {
	s.rootsMu.Lock()
	defer s.rootsMu.Unlock()
	for _, rt := range s.roots {
		if rel, ok := s.served(rt, abs); ok {
			return rt.page(rel)
		}
	}
	rt := newRoot(abs, isDir)
	rt.prefix = s.prefix(rt.dir)
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
	s.broadcast("reload")
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

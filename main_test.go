package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range map[string]string{
		"README.md":               "# Root\n",
		"notes.txt":               "plain\n",
		"docs/intro.md":           "# Intro\n",
		"docs/api/spec.markdown":  "# Spec\n",
		"docs/api/deep/deep.md":   "# Deep\n",
		"node_modules/dep/dep.md": "# Dep\n",
		".hidden/secret.md":       "# Secret\n",
		"vendor/lib/vendored.md":  "# Vendored\n",
	} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func testServer(t *testing.T, dir, entry string) *server {
	t.Helper()
	target := dir
	if entry != "" {
		target = filepath.Join(dir, entry)
	}
	s := newServer(newRoot(target, entry == "", ""), options{depth: 5, skip: defaultSkip})
	s.launch = func(string) error { return nil }
	return s
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func request(t *testing.T, s *server, method, url string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, url, nil)
	req.Host = "127.0.0.1:8080"
	if method == http.MethodPost {
		req.Header.Set(tokenHeader, s.token)
	}
	recorder := httptest.NewRecorder()
	s.routes().ServeHTTP(recorder, req)
	return recorder
}

func get(t *testing.T, s *server, url string) (int, string) {
	t.Helper()
	recorder := request(t, s, http.MethodGet, url)
	return recorder.Code, recorder.Body.String()
}

func post(t *testing.T, s *server, url string) (int, string) {
	t.Helper()
	recorder := request(t, s, http.MethodPost, url)
	return recorder.Code, recorder.Body.String()
}

func TestParseArgs(t *testing.T) {
	defaults := options{target: ".", depth: 5, skip: defaultSkip}
	with := func(change func(*options)) options {
		opts := defaults
		change(&opts)
		return opts
	}
	cases := []struct {
		args []string
		want options
	}{
		{nil, defaults},
		{[]string{"plan.md"}, with(func(o *options) { o.target = "plan.md" })},
		{[]string{"-d", "0", "docs"}, with(func(o *options) { o.target, o.depth = "docs", 0 })},
		{[]string{"docs", "--depth", "-1"}, with(func(o *options) {
			o.target, o.depth = "docs", -1
		})},
		{[]string{"-s", "target,out"}, with(func(o *options) { o.skip = []string{"target", "out"} })},
		{[]string{"-b", "plan.md"}, with(func(o *options) { o.target, o.background = "plan.md", true })},
		{[]string{"--background", "--new", "--no-open"}, with(func(o *options) {
			o.background, o.fresh, o.noOpen = true, true, true
		})},
		{[]string{"-d"}, defaults},
		{[]string{"-d", "oops"}, defaults},
	}
	for _, c := range cases {
		got, err := parseArgs(c.args)
		if err != nil {
			t.Errorf("parseArgs(%q) = %v, want no error", c.args, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseArgs(%q) = %+v, want %+v", c.args, got, c.want)
		}
	}
}

func TestParseArgsRejectsUnknownFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--stopp"},
		{"--stop-it"},
		{"-x"},
		{"docs", "--bogus"},
		{"-"},
		{"-t", "dark"},
	} {
		opts, err := parseArgs(args)
		if err == nil {
			t.Errorf("parseArgs(%q) = %+v, want an error naming the flag", args, opts)
			continue
		}
		if !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("parseArgs(%q) = %v, want the message to say which flag it was", args, err)
		}
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"./-weird.md"}, "./-weird.md"},
		{[]string{"-d", "-1", "docs"}, "docs"},
		{[]string{"-s", "-weird", "docs"}, "docs"},
	} {
		got, err := parseArgs(c.args)
		if err != nil {
			t.Errorf("parseArgs(%q) = %v, want it accepted", c.args, err)
			continue
		}
		if got.target != c.want {
			t.Errorf("parseArgs(%q) target = %q, want %q", c.args, got.target, c.want)
		}
	}
}

func TestSplitCommand(t *testing.T) {
	for _, c := range []struct {
		command string
		want    []string
	}{
		{"", nil},
		{"   ", nil},
		{"code", []string{"code"}},
		{"code -g", []string{"code", "-g"}},
		{"  code   -g  ", []string{"code", "-g"}},
		{`"C:\Program Files\Microsoft VS Code\Code.exe" -g`, []string{`C:\Program Files\Microsoft VS Code\Code.exe`, "-g"}},
		{`"/Applications/My Editor.app/Contents/MacOS/ed"`, []string{"/Applications/My Editor.app/Contents/MacOS/ed"}},
		{`ed --arg="two words"`, []string{"ed", "--arg=two words"}},
	} {
		if got := splitCommand(c.command); !slices.Equal(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
			t.Errorf("splitCommand(%q) = %q, want %q", c.command, got, c.want)
		}
	}
}

func TestOpenInEditorKeepsQuotedPathWhole(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "my editor.sh")
	log := filepath.Join(dir, "opened.txt")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + strconv.Quote(log) + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MDS_EDITOR", strconv.Quote(script)+" -g")
	if err := openInEditor(filepath.Join(dir, "plan.md")); err != nil {
		t.Skipf("cannot run a shell script here: %v", err)
	}
	var opened []byte
	for range 100 {
		if opened, _ = os.ReadFile(log); len(opened) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	want := "-g\n" + filepath.Join(dir, "plan.md") + "\n"
	if string(opened) != want {
		t.Errorf("editor received %q, want %q", opened, want)
	}
}

func TestFavicon(t *testing.T) {
	s := testServer(t, fixture(t), "")
	if _, body := get(t, s, "/"); !strings.Contains(body, `rel="icon" href="/_static/favicon.svg"`) {
		t.Error("the page does not point at the favicon, so browsers will probe /favicon.ico instead")
	}
	status, icon := get(t, s, "/_static/favicon.svg")
	if status != http.StatusOK {
		t.Fatalf("GET /_static/favicon.svg = %d", status)
	}
	for _, want := range []string{"viewBox=\"0 0 64 64\"", ">.MD<", "textLength="} {
		if !strings.Contains(icon, want) {
			t.Errorf("favicon missing %q", want)
		}
	}
}

func TestMermaidLoadsOnlyWhereItIsUsed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "plain.md", "# Plain\n\n```go\nfunc main() {}\n```\n")
	writeFile(t, dir, "chart.md", "# Chart\n\n```mermaid\ngraph TD\n  A --> B\n```\n")
	s := testServer(t, dir, "")

	if _, body := get(t, s, "/plain.md"); strings.Contains(body, "mermaid.min.js") {
		t.Error("a page with no diagram should not pull in 3.5 MB of mermaid")
	}
	_, body := get(t, s, "/chart.md")
	if !strings.Contains(body, "mermaid.min.js") {
		t.Fatal("a page with a diagram must load mermaid")
	}
	if !strings.Contains(body, `<script nonce="`) {
		t.Error("the mermaid tag must carry the nonce or the csp will block it")
	}
	if before, _, _ := strings.Cut(body, "app.js"); !strings.Contains(before, "mermaid.min.js") {
		t.Error("mermaid must come before app.js, which calls mermaid.initialize")
	}
}

func TestServeContentDirMode(t *testing.T) {
	s := testServer(t, fixture(t), "")
	cases := []struct {
		url    string
		status int
		want   string
	}{
		{"/", http.StatusOK, "Root"},
		{"/", http.StatusOK, `id="burger"`},
		{"/docs/intro.md", http.StatusOK, "Intro"},
		{"/docs/intro.md", http.StatusOK, `id="edit" title="Open in editor" data-path="/docs/intro.md"`},
		{"/docs/api/spec.markdown", http.StatusOK, "Spec"},
		{"/notes.txt", http.StatusNotFound, "no such file"},
		{"/_static/app.css", http.StatusOK, "--code-bg"},
		{"/missing.md", http.StatusNotFound, "no such file"},
		{"/../../../../etc/passwd", http.StatusMovedPermanently, "/etc/passwd"},
		{"/etc/passwd", http.StatusNotFound, ""},
	}
	for _, c := range cases {
		status, body := get(t, s, c.url)
		if status != c.status || !strings.Contains(body, c.want) {
			t.Errorf("GET %s = %d, want %d containing %q", c.url, status, c.status, c.want)
		}
		if strings.Contains(body, "root:") {
			t.Errorf("GET %s escaped the root directory", c.url)
		}
	}
	if status, _ := get(t, s, "/%2e%2e/%2e%2e/etc/hosts"); status == http.StatusOK {
		t.Error("encoded traversal must not be served")
	}
	if _, body := get(t, s, "/missing.md"); strings.Contains(body, `id="edit"`) {
		t.Error("404 page should not offer the edit button")
	}
}

func TestServeContentFileMode(t *testing.T) {
	s := testServer(t, fixture(t), "docs/intro.md")
	status, body := get(t, s, "/")
	if status != http.StatusOK || !strings.Contains(body, "Intro") {
		t.Errorf("GET / = %d, want 200 with the entry file", status)
	}
	if strings.Contains(body, `id="burger"`) {
		t.Error("file mode should not render the burger")
	}
}

func TestServeEdit(t *testing.T) {
	root := fixture(t)
	s := testServer(t, root, "")
	var opened []string
	s.launch = func(target string) error {
		opened = append(opened, target)
		return nil
	}
	for _, c := range []struct {
		query  string
		status int
	}{
		{"docs/intro.md", http.StatusNoContent},
		{"/docs/intro.md", http.StatusNoContent},
		{"../../../etc/passwd.md", http.StatusBadRequest},
		{"notes.txt", http.StatusBadRequest},
		{"", http.StatusBadRequest},
		{"missing.md", http.StatusNotFound},
	} {
		if status, _ := post(t, s, "/_edit?path="+c.query); status != c.status {
			t.Errorf("POST /_edit?path=%s = %d, want %d", c.query, status, c.status)
		}
	}
	want := filepath.Join(root, "docs", "intro.md")
	if !slices.Equal(opened, []string{want, want}) {
		t.Errorf("launched %q, want the entry file twice", opened)
	}
	s.launch = func(string) error { return errors.New("no editor") }
	if status, _ := post(t, s, "/_edit?path=README.md"); status != http.StatusInternalServerError {
		t.Errorf("failing editor = %d, want 500", status)
	}
	if status, _ := get(t, s, "/_edit?path=README.md"); status != http.StatusNotFound {
		t.Errorf("GET /_edit = %d, want 404", status)
	}
	if len(opened) != 2 {
		t.Errorf("editor launched %d times, want a GET to have been ignored", len(opened))
	}
}

func TestAddRoot(t *testing.T) {
	dir := fixture(t)
	outside := t.TempDir()
	plan := writeFile(t, outside, "plan.md", "# Plan\n")
	s := testServer(t, dir, "")

	if page := s.addRoot(filepath.Join(dir, "docs", "intro.md"), false); page != "/docs/intro.md" {
		t.Errorf("adding a file already in the tree = %q, want /docs/intro.md", page)
	}
	if page := s.addRoot(dir, true); page != "/" {
		t.Errorf("adding the served root = %q, want /", page)
	}
	if len(s.roots) != 1 {
		t.Fatalf("roots = %d, want the served paths to be reused", len(s.roots))
	}
	if page := s.addRoot(plan, false); page != "/_r1/plan.md" {
		t.Fatalf("adding an outside file = %q, want /_r1/plan.md", page)
	}
	if page := s.addRoot(plan, false); page != "/_r1/plan.md" || len(s.roots) != 2 {
		t.Errorf("adding it twice = %q with %d roots, want the same page and 2 roots", page, len(s.roots))
	}
	if status, body := get(t, s, "/_r1/plan.md"); status != http.StatusOK || !strings.Contains(body, "Plan") {
		t.Errorf("GET /_r1/plan.md = %d, want the added file", status)
	}
	if status, body := get(t, s, "/_r1"); status != http.StatusOK || !strings.Contains(body, "Plan") {
		t.Errorf("GET /_r1 = %d, want the entry of that root", status)
	}
	if _, body := get(t, s, "/_r1/plan.md"); !strings.Contains(body, `id="burger"`) {
		t.Error("a second root should bring the sidebar up")
	}
	if status, _ := get(t, s, "/_r9/plan.md"); status != http.StatusNotFound {
		t.Errorf("unknown root prefix = %d, want 404", status)
	}
}

func TestServeContentFromCache(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "plan.md", "# Plan\n")
	writeFile(t, dir, "cold.md", "# Cold\n")
	s := testServer(t, dir, "")

	if status, body := get(t, s, "/plan.md"); status != http.StatusOK || strings.Contains(body, "data-stale") {
		t.Fatalf("GET /plan.md = %d, want a fresh 200", status)
	}
	s.rootFiles(s.roots[0])
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	status, body := get(t, s, "/plan.md")
	if status != http.StatusOK || !strings.Contains(body, "Plan") {
		t.Errorf("GET /plan.md after the directory vanished = %d, want the cached copy", status)
	}
	if !strings.Contains(body, "data-stale") {
		t.Error("a cached page must be marked stale")
	}
	if status, _ := get(t, s, "/cold.md"); status != http.StatusNotFound {
		t.Errorf("GET /cold.md = %d, want 404: it was never read, so nothing is cached", status)
	}
	if _, body := get(t, s, "/_tree"); !strings.Contains(body, "plan.md") {
		t.Error("the tree should fall back to the last scan of an unreadable root")
	}
}

func TestCacheEviction(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, dir, "")
	for i := range cachedFiles + 10 {
		s.remember(filepath.Join(dir, fmt.Sprintf("%d.md", i)), cached{source: []byte("# body\n")})
	}
	if len(s.cache) != cachedFiles || len(s.order) != cachedFiles {
		t.Errorf("cache holds %d files, want %d", len(s.cache), cachedFiles)
	}
	if _, ok := s.recall(filepath.Join(dir, "0.md")); ok {
		t.Error("the oldest entry should have been evicted")
	}
	if _, ok := s.recall(filepath.Join(dir, "70.md")); !ok {
		t.Error("the newest entry should still be cached")
	}
	s.remember(filepath.Join(dir, "huge.md"), cached{source: make([]byte, cachedBytes+1)})
	if _, ok := s.recall(filepath.Join(dir, "huge.md")); !ok || len(s.cache) != 1 {
		t.Errorf("a file over the byte budget should stay alone in the cache, got %d entries", len(s.cache))
	}
}

func TestRepeatedRequestsReuseTheCache(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "plan.md", "# Plan\n\n```go\nfunc main() {}\n```\n")
	s := testServer(t, dir, "")

	get(t, s, "/plan.md")
	first, ok := s.recall(file)
	if !ok {
		t.Fatal("reading a file should cache it")
	}
	get(t, s, "/plan.md")
	again, _ := s.recall(file)
	if &first.source[0] != &again.source[0] {
		t.Error("an unchanged file was re-read into a new buffer")
	}
	if first.content != again.content {
		t.Error("an unchanged file was rendered twice")
	}

	if err := os.WriteFile(file, []byte("# Edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, body := get(t, s, "/plan.md"); !strings.Contains(body, "Edited") {
		t.Error("the cache served a stale render after the file changed")
	}

	s.cacheMu.Lock()
	cachedTree := s.trees[""]
	s.cacheMu.Unlock()
	get(t, s, "/_tree")
	get(t, s, "/_tree")
	s.cacheMu.Lock()
	sameTree := s.trees[""] == cachedTree
	s.cacheMu.Unlock()
	if cachedTree != nil && !sameTree {
		t.Error("/_tree walked the filesystem again without anything changing")
	}
	s.rescan()
	get(t, s, "/_tree")
	s.cacheMu.Lock()
	rescanned := s.trees[""] != cachedTree
	s.cacheMu.Unlock()
	if !rescanned {
		t.Error("/_tree kept the old tree after the watcher reported a change")
	}
}

func TestDropRoot(t *testing.T) {
	dir := fixture(t)
	plan := writeFile(t, t.TempDir(), "plan.md", "# Plan\n")
	s := testServer(t, dir, "")
	s.addRoot(plan, false)
	get(t, s, "/_r1/plan.md")
	if _, ok := s.recall(plan); !ok {
		t.Fatal("reading a file should cache it")
	}

	drop := func(query string) int {
		status, _ := post(t, s, "/_drop"+query)
		return status
	}
	if code := drop("?root=_r9"); code != http.StatusBadRequest {
		t.Errorf("dropping an unknown root = %d, want 400", code)
	}
	if code := drop("?root=_r1"); code != http.StatusNoContent {
		t.Fatalf("POST /_drop?root=_r1 = %d, want 204", code)
	}
	if len(s.roots) != 1 {
		t.Errorf("roots = %d, want the dropped one gone", len(s.roots))
	}
	if _, ok := s.recall(plan); ok {
		t.Error("dropping a root must clear its cached files")
	}
	if status, _ := get(t, s, "/_r1/plan.md"); status != http.StatusNotFound {
		t.Errorf("GET /_r1/plan.md after the drop = %d, want 404", status)
	}
	if code := drop("?root="); code != http.StatusBadRequest {
		t.Errorf("dropping the last root = %d, want 400", code)
	}
	if code := drop(""); code != http.StatusBadRequest {
		t.Errorf("POST /_drop without a root = %d, want 400", code)
	}
}

func TestDroppedPrefixIsNotReused(t *testing.T) {
	dir := fixture(t)
	first := writeFile(t, t.TempDir(), "first.md", "# First\n")
	second := writeFile(t, t.TempDir(), "second.md", "# Second\n")
	s := testServer(t, dir, "")
	if page := s.addRoot(first, false); page != "/_r1/first.md" {
		t.Fatalf("first added root = %q", page)
	}
	s.dropRoot("_r1")
	if page := s.addRoot(second, false); page != "/_r2/second.md" {
		t.Errorf("root added after a drop = %q, want /_r2/second.md so old tabs keep their urls", page)
	}
}

func TestShortPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for dir, want := range map[string]string{
		filepath.Join(home, "proj", "mds"): "~/proj/mds",
		home:                               "~",
		home + "-not-mine/docs":            home + "-not-mine/docs",
		"/tmp/plans":                       "/tmp/plans",
	} {
		if got := shortPath(dir); got != want {
			t.Errorf("shortPath(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestTreeRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "README.md", "# Root\n")
	plan := writeFile(t, t.TempDir(), "plan.md", "# Plan\n")
	s := testServer(t, dir, "")

	if _, body := get(t, s, "/_tree"); strings.Contains(body, `"type":"root"`) {
		t.Error("a single root should not be wrapped in a labelled group")
	}
	s.addRoot(plan, false)
	_, body := get(t, s, "/_tree")
	var tree struct{ Nodes []*node }
	if err := json.Unmarshal([]byte(body), &tree); err != nil {
		t.Fatal(err)
	}
	if len(tree.Nodes) != 2 {
		t.Fatalf("nodes = %+v, want one group per root", tree.Nodes)
	}
	if tree.Nodes[0].Type != "root" || tree.Nodes[0].Name != "~/project" {
		t.Errorf("first group = %+v, want a root named ~/project", tree.Nodes[0])
	}
	if tree.Nodes[1].Name != shortPath(filepath.Dir(plan)) || len(tree.Nodes[1].Children) != 1 {
		t.Errorf("second group = %+v, want the directory the file came from", tree.Nodes[1])
	}
	if tree.Nodes[1].Children[0].Path != "_r1/plan.md" {
		t.Errorf("child path = %q, want the prefixed path", tree.Nodes[1].Children[0].Path)
	}
}

func TestServeAdd(t *testing.T) {
	dir := fixture(t)
	plan := writeFile(t, t.TempDir(), "plan.md", "# Plan\n")
	s := testServer(t, dir, "")
	s.port = "8080"
	add := func(target string) (int, string) {
		return post(t, s, "/_add?path="+url.QueryEscape(target))
	}
	if status, body := add(plan); status != http.StatusOK || body != s.origin()+"/_r1/plan.md" {
		t.Errorf("POST /_add = %d %q, want the full URL of the added file", status, body)
	}
	if status, _ := add("relative.md"); status != http.StatusBadRequest {
		t.Errorf("relative path = %d, want 400", status)
	}
	if status, _ := add(filepath.Join(dir, "missing.md")); status != http.StatusBadRequest {
		t.Errorf("missing path = %d, want 400", status)
	}
	if status, _ := get(t, s, "/_add?path="+plan); status != http.StatusNotFound || len(s.roots) != 2 {
		t.Errorf("GET /_add = %d with %d roots, want 404 and no root added", status, len(s.roots))
	}
}

func TestAddSendsAnOpenTabToTheNewPage(t *testing.T) {
	s := testServer(t, fixture(t), "")
	s.port = "8080"
	plan := writeFile(t, t.TempDir(), "plan.md", "# Plan\n")

	recorder := request(t, s, http.MethodPost, "/_add?path="+url.QueryEscape(plan))
	if got := recorder.Header().Get(tabsHeader); got != "0" {
		t.Errorf("%s = %q with nothing listening, want 0 so the cli opens a browser", tabsHeader, got)
	}

	updates := make(chan string, 1)
	s.mu.Lock()
	s.subs[updates] = struct{}{}
	s.mu.Unlock()

	next := writeFile(t, t.TempDir(), "next.md", "# Next\n")
	recorder = request(t, s, http.MethodPost, "/_add?path="+url.QueryEscape(next))
	if got := recorder.Header().Get(tabsHeader); got != "1" {
		t.Errorf("%s = %q with a tab open, want 1 so the cli leaves the browser alone", tabsHeader, got)
	}
	select {
	case message := <-updates:
		if message != "go /_r2/next.md" {
			t.Errorf("the open tab got %q, want it sent to the new page rather than a second tab opening", message)
		}
	case <-time.After(time.Second):
		t.Fatal("the open tab was told nothing, so the new file would go unnoticed")
	}
}

func TestServeStop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := testServer(t, t.TempDir(), "")
	s.port = "8080"
	stopped := make(chan struct{})
	s.shutdown = func() { close(stopped) }
	addInstance(s.port, s.token)
	if status, _ := post(t, s, "/_stop"); status != http.StatusNoContent {
		t.Errorf("POST /_stop = %d, want 204", status)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("/_stop did not shut the server down")
	}
	if len(readInstances()) != 0 {
		t.Error("/_stop left the instance behind in the state file")
	}
}

func TestServeTree(t *testing.T) {
	root := fixture(t)
	s := testServer(t, root, "")
	status, body := get(t, s, "/_tree")
	if status != http.StatusOK {
		t.Fatalf("GET /_tree = %d", status)
	}
	var tree struct {
		Root  string  `json:"root"`
		Nodes []*node `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(body), &tree); err != nil {
		t.Fatal(err)
	}
	if tree.Root != filepath.Base(root) {
		t.Errorf("root = %q, want %q", tree.Root, filepath.Base(root))
	}
	if len(tree.Nodes) != 2 || tree.Nodes[0].Name != "docs" || tree.Nodes[1].Name != "README.md" {
		t.Errorf("nodes = %+v, want docs then README.md", tree.Nodes)
	}
}

func TestServeEvents(t *testing.T) {
	s := testServer(t, t.TempDir(), "")
	httpServer := httptest.NewServer(s.routes())
	defer httpServer.Close()
	response, err := http.Get(httpServer.URL + "/_events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("content type = %q, want text/event-stream", got)
	}
	done := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(response.Body).ReadString('\n')
		done <- line
	}()
	for {
		s.broadcast("reload")
		select {
		case line := <-done:
			if strings.TrimSpace(line) != "data: reload" {
				t.Errorf("event = %q, want data: reload", line)
			}
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestWatchReload(t *testing.T) {
	root := fixture(t)
	s := testServer(t, root, "")
	updates := make(chan string, 1)
	s.mu.Lock()
	s.subs[updates] = struct{}{}
	s.mu.Unlock()
	go s.watch()
	time.Sleep(200 * time.Millisecond)
	s.cacheMu.Lock()
	files := slices.Clone(s.trees[""].files)
	s.cacheMu.Unlock()
	if !slices.Contains(files, "README.md") {
		t.Errorf("the watcher should warm the tree fallback, got %q", files)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "intro.md"), []byte("# Edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("no reload broadcast after editing a watched file")
	}
}

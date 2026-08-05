package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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

func testServer(t *testing.T, root, entry string) *server {
	t.Helper()
	return &server{
		root:    root,
		entry:   entry,
		dirMode: entry == "",
		depth:   5,
		theme:   "light",
		skip:    defaultSkip,
		launch:  func(string) error { return nil },
		tmpl:    template.Must(template.ParseFS(assetFS, "assets/page.html")),
		subs:    map[chan struct{}]struct{}{},
	}
}

func get(t *testing.T, s *server, url string) (int, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	s.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, url, nil))
	return recorder.Code, recorder.Body.String()
}

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args []string
		want options
	}{
		{nil, options{".", 5, "light", defaultSkip, false}},
		{[]string{"plan.md"}, options{"plan.md", 5, "light", defaultSkip, false}},
		{[]string{"-d", "0", "docs"}, options{"docs", 0, "light", defaultSkip, false}},
		{[]string{"docs", "--depth", "-1", "--theme", "dark"}, options{"docs", -1, "dark", defaultSkip, false}},
		{[]string{"-s", "target,out"}, options{".", 5, "light", []string{"target", "out"}, false}},
		{[]string{"-b", "plan.md"}, options{"plan.md", 5, "light", defaultSkip, true}},
		{[]string{"--background"}, options{".", 5, "light", defaultSkip, true}},
		{[]string{"-t"}, options{".", 5, "light", defaultSkip, false}},
		{[]string{"-d", "oops"}, options{".", 5, "light", defaultSkip, false}},
	}
	for _, c := range cases {
		got := parseArgs(c.args)
		if got.target != c.want.target || got.depth != c.want.depth || got.theme != c.want.theme ||
			!slices.Equal(got.skip, c.want.skip) || got.background != c.want.background {
			t.Errorf("parseArgs(%q) = %+v, want %+v", c.args, got, c.want)
		}
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
		{"/docs/intro.md", http.StatusOK, `id="edit" title="Open in editor" data-path="docs/intro.md"`},
		{"/docs/api/spec.markdown", http.StatusOK, "Spec"},
		{"/notes.txt", http.StatusOK, "plain"},
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
		if status, _ := get(t, s, "/_edit?path="+c.query); status != c.status {
			t.Errorf("GET /_edit?path=%s = %d, want %d", c.query, status, c.status)
		}
	}
	want := filepath.Join(root, "docs", "intro.md")
	if !slices.Equal(opened, []string{want, want}) {
		t.Errorf("launched %q, want the entry file twice", opened)
	}
	s.launch = func(string) error { return errors.New("no editor") }
	if status, _ := get(t, s, "/_edit?path=README.md"); status != http.StatusInternalServerError {
		t.Errorf("failing editor = %d, want 500", status)
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
		s.broadcast()
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
	updates := make(chan struct{}, 1)
	s.subs[updates] = struct{}{}
	go s.watch()
	time.Sleep(200 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(root, "docs", "intro.md"), []byte("# Edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("no reload broadcast after editing a watched file")
	}
}

package main

import (
	"bufio"
	"encoding/json"
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
		args  []string
		path  string
		depth int
		theme string
		skip  []string
	}{
		{nil, ".", 5, "light", defaultSkip},
		{[]string{"plan.md"}, "plan.md", 5, "light", defaultSkip},
		{[]string{"-d", "0", "docs"}, "docs", 0, "light", defaultSkip},
		{[]string{"docs", "--depth", "-1", "--theme", "dark"}, "docs", -1, "dark", defaultSkip},
		{[]string{"-s", "target,out"}, ".", 5, "light", []string{"target", "out"}},
		{[]string{"-t"}, ".", 5, "light", defaultSkip},
		{[]string{"-d", "oops"}, ".", 5, "light", defaultSkip},
	}
	for _, c := range cases {
		path, depth, theme, skip := parseArgs(c.args)
		if path != c.path || depth != c.depth || theme != c.theme || !slices.Equal(skip, c.skip) {
			t.Errorf("parseArgs(%q) = %q %d %q %q, want %q %d %q %q",
				c.args, path, depth, theme, skip, c.path, c.depth, c.theme, c.skip)
		}
	}
}

func TestIsMarkdown(t *testing.T) {
	for name, want := range map[string]bool{
		"a.md": true, "a.MD": true, "a.markdown": true,
		"dir/b.md": true, "a.txt": false, "a": false, "md": false,
	} {
		if got := isMarkdown(name); got != want {
			t.Errorf("isMarkdown(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestScan(t *testing.T) {
	root := fixture(t)
	cases := []struct {
		depth int
		skip  []string
		files []string
	}{
		{5, defaultSkip, []string{"README.md", "docs/api/deep/deep.md", "docs/api/spec.markdown", "docs/intro.md"}},
		{0, defaultSkip, []string{"README.md"}},
		{1, defaultSkip, []string{"README.md", "docs/intro.md"}},
		{-1, defaultSkip, []string{"README.md", "docs/api/deep/deep.md", "docs/api/spec.markdown", "docs/intro.md"}},
		{5, append(slices.Clone(defaultSkip), "docs"), []string{"README.md"}},
	}
	for _, c := range cases {
		_, files := scan(root, c.depth, c.skip)
		if !slices.Equal(files, c.files) {
			t.Errorf("scan(depth=%d, skip=%q) = %q, want %q", c.depth, c.skip, files, c.files)
		}
	}
	dirs, _ := scan(root, 5, defaultSkip)
	if len(dirs) != 4 {
		t.Errorf("scan dirs = %q, want root, docs, docs/api, docs/api/deep", dirs)
	}
}

func TestBuildTree(t *testing.T) {
	tree := buildTree([]string{"z.md", "docs/b.md", "docs/a/x.md", "a.md"})
	encoded, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"dir","name":"docs","path":"docs","children":[` +
		`{"type":"dir","name":"a","path":"docs/a","children":[{"type":"file","name":"x.md","path":"docs/a/x.md"}]},` +
		`{"type":"file","name":"b.md","path":"docs/b.md"}]},` +
		`{"type":"file","name":"a.md","path":"a.md"},` +
		`{"type":"file","name":"z.md","path":"z.md"}]`
	if string(encoded) != want {
		t.Errorf("buildTree =\n%s\nwant\n%s", encoded, want)
	}
}

func TestRender(t *testing.T) {
	source := "# Title\n\n```mermaid\ngraph TD\n  A --> B\n```\n\n```go\nfunc main() {}\n```\n\n" +
		"| a | b |\n|---|---|\n| 1 | 2 |\n\n- [x] done\n"
	html := string(render([]byte(source)))
	for _, want := range []string{`<pre class="mermaid">`, "A --&gt; B", `class="chroma"`, "<table>", `type="checkbox"`} {
		if !strings.Contains(html, want) {
			t.Errorf("render() missing %q in\n%s", want, html)
		}
	}
	if strings.Contains(html, "language-mermaid") {
		t.Errorf("render() left an unconverted mermaid block in\n%s", html)
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

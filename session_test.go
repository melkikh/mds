package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func unlock(t *testing.T, s *server) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/_auth", strings.NewReader(s.token))
	req.Host = "127.0.0.1:8080"
	recorder := httptest.NewRecorder()
	s.routes().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("POST /_auth = %d, so the browser cannot unlock mds", recorder.Code)
	}
	cookies := (&http.Response{Header: recorder.Header()}).Cookies()
	if len(cookies) != 1 {
		t.Fatalf("POST /_auth set %d cookies, so there is no browser session to restore", len(cookies))
	}
	return cookies[0]
}

func readWithCookie(t *testing.T, s *server, cookie *http.Cookie) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "127.0.0.1:8080"
	req.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	s.routes().ServeHTTP(recorder, req)
	return recorder.Code
}

func TestLoginServerRestoresBrowserSessionsFromHashes(t *testing.T) {
	isolateCache(t)
	opts := defaultOptions()
	opts.service = serviceRun

	first := newServer(nil, opts)
	oldCookie := unlock(t, first)
	stored, err := os.ReadFile(serviceSessionsFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), oldCookie.Value) {
		t.Fatal("the login server wrote the browser session itself to disk instead of only its hash")
	}
	hashes := readSessionHashes(serviceSessionsFile())
	if want := hashSession(oldCookie.Value); len(hashes) != 1 || hashes[0] != want {
		t.Fatalf("saved session verifiers = %x, so the restarted service cannot validate the browser cookie", hashes)
	}
	info, err := os.Stat(serviceSessionsFile())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("session verifier mode = %v, so trusted-browser state is exposed more broadly than the instance keys", info.Mode().Perm())
	}

	second := newServer(nil, opts)
	if first.token == second.token || first.action == second.action {
		t.Fatal("a login-server restart reused a master or action key while preserving only the browser session")
	}
	if code := readWithCookie(t, second, oldCookie); code != http.StatusOK {
		t.Fatalf("GET / after a login-server restart = %d, so the pinned tab asks for the key again", code)
	}
	newCookie := unlock(t, second)

	third := newServer(nil, opts)
	for name, cookie := range map[string]*http.Cookie{"old browser": oldCookie, "new browser": newCookie} {
		if code := readWithCookie(t, third, cookie); code != http.StatusOK {
			t.Errorf("GET / from the %s after another restart = %d, so authorizing one browser displaced another", name, code)
		}
	}
}

func TestOrdinaryServerDoesNotTrustAnEarlierSession(t *testing.T) {
	first := newServer(nil, defaultOptions())
	cookie := unlock(t, first)
	second := newServer(nil, defaultOptions())
	if code := readWithCookie(t, second, cookie); code != http.StatusUnauthorized {
		t.Errorf("GET / on another ordinary server = %d, want 401 so only the login service remembers browsers", code)
	}
}

func TestDroppingServiceSessionsRevokesTheSavedBrowser(t *testing.T) {
	isolateCache(t)
	opts := defaultOptions()
	opts.service = serviceRun
	cookie := unlock(t, newServer(nil, opts))
	if err := dropServiceSessions(); err != nil {
		t.Fatal(err)
	}
	if code := readWithCookie(t, newServer(nil, opts), cookie); code != http.StatusUnauthorized {
		t.Errorf("GET / after removing saved sessions = %d, want 401 so --remove revokes the browser", code)
	}
}

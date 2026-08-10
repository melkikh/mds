package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func isolateCache(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
}

func captureStdout(t *testing.T, run func()) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = write
	run()
	os.Stdout = saved
	write.Close()
	out, _ := io.ReadAll(read)
	return string(out)
}

func livePort(t *testing.T, live *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(live.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestInstanceState(t *testing.T) {
	isolateCache(t)
	if len(readInstances()) != 0 {
		t.Fatal("readInstances found servers in an empty cache")
	}
	addInstance("9998", "first-token")
	addInstance("9999", "second-token")
	running := readInstances()
	if len(running) != 2 || running[1].port != "9999" {
		t.Fatalf("readInstances() = %+v, want both servers", running)
	}
	if running[1].token != "second-token" {
		t.Errorf("token = %q, want it round-tripped so the cli can authenticate", running[1].token)
	}
	if running[1].origin() != "http://127.0.0.1:9999" {
		t.Errorf("origin() = %q, want it built from the port alone", running[1].origin())
	}
	info, err := os.Stat(filepath.Join(instancesDir(), "9999"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("state file mode = %v, want 0600: it holds the server's token", info.Mode().Perm())
	}
	dropInstance("9998")
	if running = readInstances(); len(running) != 1 || running[0].port != "9999" {
		t.Errorf("after dropInstance = %+v, want only the second server", running)
	}
}

func TestInstanceStateIgnoresJunk(t *testing.T) {
	isolateCache(t)
	addInstance("9999", "real-token")
	for _, name := range []string{"9999@evil.example.com", "instances.json", "..", "notaport"} {
		if err := os.WriteFile(filepath.Join(instancesDir(), name), []byte("x"), 0o600); err != nil {
			continue
		}
	}
	running := readInstances()
	if len(running) != 1 || running[0].port != "9999" {
		t.Errorf("readInstances() = %+v, want only the numeric entry", running)
	}
}

func TestLegacyStateIsRemovedOnAnyRun(t *testing.T) {
	isolateCache(t)
	addInstance("9999", "token")
	legacy := filepath.Join(filepath.Dir(instancesDir()), "instances.json")
	body := `[{"url":"http://127.0.0.1:53800","token":"leaked"}]`
	if err := os.WriteFile(legacy, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	running := readInstances()
	if _, err := os.Stat(legacy); err == nil {
		t.Error("the pre-0.2 state file still sits there with its tokens at 0644, " +
			"even though a run that only joins never calls addInstance")
	}
	if len(running) != 1 || running[0].port != "9999" {
		t.Errorf("readInstances() = %+v, want only the new-format entry", running)
	}
}

func TestCallSendsTheToken(t *testing.T) {
	reached := make(chan string, 1)
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- r.Header.Get(tokenHeader)
	}))
	defer live.Close()
	if _, err := call(instance{port: livePort(t, live), token: "secret"}, "/_stop"); err != nil {
		t.Fatalf("call() failed: %v", err)
	}
	if got := <-reached; got != "secret" {
		t.Errorf("token header = %q, want it forwarded", got)
	}
}

func TestAddToRunning(t *testing.T) {
	isolateCache(t)
	plan := writeFile(t, t.TempDir(), "plan.md", "# Plan\n")
	if _, _, ok := addToRunning(plan); ok {
		t.Error("addToRunning succeeded with no state file")
	}
	addInstance("1", "dead-token")
	if _, _, ok := addToRunning(plan); ok {
		t.Error("addToRunning succeeded against a dead server")
	}
	if len(readInstances()) != 0 {
		t.Error("a server that refused the connection was left in the state file")
	}

	s := testServer(t, fixture(t), "")
	live := httptest.NewServer(s.routes())
	defer live.Close()
	s.port = livePort(t, live)
	addInstance(s.port, s.token)
	page, _, ok := addToRunning(plan)
	if !ok || page != live.URL+"/_r1/plan.md" {
		t.Fatalf("addToRunning() = %q %v, want the url of the added file", page, ok)
	}
	if _, _, ok := addToRunning(filepath.Join(t.TempDir(), "missing.md")); ok {
		t.Error("addToRunning succeeded for a path the server rejected")
	}
	if len(readInstances()) != 1 {
		t.Error("a live server was dropped from the state file")
	}
}

func TestSlowServerIsNotDroppedAsDead(t *testing.T) {
	isolateCache(t)
	blocked := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	defer slow.Close()
	defer close(blocked)
	talk.Timeout = 100 * time.Millisecond
	defer func() { talk.Timeout = 2 * time.Second }()

	port := livePort(t, slow)
	addInstance(port, "token")
	if _, _, ok := addToRunning(writeFile(t, t.TempDir(), "plan.md", "# Plan\n")); ok {
		t.Error("addToRunning claimed a hung server took the path")
	}
	if len(readInstances()) != 1 {
		t.Error("a server that timed out was treated as dead and dropped")
	}
}

func TestStopRunning(t *testing.T) {
	isolateCache(t)
	s := testServer(t, t.TempDir(), "")
	stopped := make(chan struct{}, 1)
	s.shutdown = func() { stopped <- struct{}{} }
	live := httptest.NewServer(s.routes())
	defer live.Close()
	s.port = livePort(t, live)
	addInstance(s.port, s.token)
	addInstance("1", "dead-token")
	out := captureStdout(t, stopRunning)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Error("stopRunning did not reach the live server")
	}
	if !strings.Contains(out, "stopped 1 server") {
		t.Errorf("stopRunning printed %q, want it to own up to the server it just stopped", strings.TrimSpace(out))
	}
	if len(readInstances()) != 0 {
		t.Errorf("stopRunning left %+v behind", readInstances())
	}
}

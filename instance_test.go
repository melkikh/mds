package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func isolateCache(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
}

func TestInstanceFile(t *testing.T) {
	isolateCache(t)
	if len(readInstances()) != 0 {
		t.Fatal("readInstances found servers in an empty cache")
	}
	addInstance("http://127.0.0.1:9998")
	addInstance("http://127.0.0.1:9999")
	running := readInstances()
	if len(running) != 2 || running[1].URL != "http://127.0.0.1:9999" || running[1].Pid != os.Getpid() {
		t.Fatalf("readInstances() = %+v, want both servers with this pid", running)
	}
	dropInstance("http://127.0.0.1:9998")
	if running = readInstances(); len(running) != 1 || running[0].URL != "http://127.0.0.1:9999" {
		t.Errorf("after dropInstance = %+v, want only the second server", running)
	}
}

func TestAddToRunning(t *testing.T) {
	isolateCache(t)
	plan := writeFile(t, t.TempDir(), "plan.md", "# Plan\n")
	if _, ok := addToRunning(plan); ok {
		t.Error("addToRunning succeeded with no state file")
	}
	addInstance("http://127.0.0.1:1")
	if _, ok := addToRunning(plan); ok {
		t.Error("addToRunning succeeded against a dead server")
	}
	if len(readInstances()) != 0 {
		t.Error("a dead server was left in the state file")
	}

	s := testServer(t, fixture(t), "")
	live := httptest.NewServer(s.routes())
	defer live.Close()
	s.url = live.URL
	addInstance(live.URL)
	page, ok := addToRunning(plan)
	if !ok || page != live.URL+"/_r1/plan.md" {
		t.Fatalf("addToRunning() = %q %v, want the url of the added file", page, ok)
	}
	if _, ok := addToRunning(filepath.Join(t.TempDir(), "missing.md")); ok {
		t.Error("addToRunning succeeded for a path the server rejected")
	}
	if len(readInstances()) != 1 {
		t.Error("a live server was dropped from the state file")
	}
}

func TestStopRunning(t *testing.T) {
	isolateCache(t)
	s := testServer(t, t.TempDir(), "")
	stopped := make(chan struct{}, 1)
	s.shutdown = func() { stopped <- struct{}{} }
	live := httptest.NewServer(s.routes())
	defer live.Close()
	s.url = live.URL
	addInstance(live.URL)
	addInstance("http://127.0.0.1:1")
	stopRunning()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Error("stopRunning did not reach the live server")
	}
	if len(readInstances()) != 0 {
		t.Errorf("stopRunning left %+v behind", readInstances())
	}
}

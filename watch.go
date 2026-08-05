package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

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

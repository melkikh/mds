package main

import (
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/fsnotify/fsnotify"
)

func (s *server) serveEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	stream := http.NewResponseController(w)
	_ = stream.Flush()
	updates := make(chan string, 1)
	s.mu.Lock()
	s.subs[updates] = struct{}{}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.subs, updates); s.mu.Unlock() }()
	for {
		select {
		case <-r.Context().Done():
			return
		case message := <-updates:
			fmt.Fprintf(w, "data: %s\n\n", message)
			_ = stream.Flush()
		}
	}
}

func (s *server) broadcast(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for updates := range s.subs {
		select {
		case <-updates:
		default:
		}
		select {
		case updates <- message:
		default:
		}
	}
}

func (s *server) tabs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

func (s *server) watch() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	defer watcher.Close()
	s.rootsMu.Lock()
	s.watcher = watcher
	s.watchRoots(true)
	for _, rt := range s.roots {
		s.rootFiles(rt)
	}
	s.rootsMu.Unlock()
	debounce := time.AfterFunc(time.Hour, func() { s.broadcast("reload") })
	debounce.Stop()
	remount := time.NewTicker(30 * time.Second)
	defer remount.Stop()
	for {
		select {
		case <-remount.C:
			s.rootsMu.Lock()
			s.watchRoots(false)
			s.rootsMu.Unlock()
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !event.Has(fsnotify.Write) {
				s.rescan()
			}
			if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
				s.rootsMu.Lock()
				s.watchRoots(true)
				s.rootsMu.Unlock()
			} else if !s.watched(event.Name) {
				continue
			}
			debounce.Reset(100 * time.Millisecond)
		case <-watcher.Errors:
		}
	}
}

func (s *server) watchRoots(rewatch bool) {
	for _, rt := range s.roots {
		_, err := os.Stat(rt.dir)
		returned := err == nil && !rt.mounted
		rt.mounted = err == nil
		if rewatch || returned {
			s.addWatches(rt)
		}
		if returned {
			s.broadcast("reload")
		}
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
	for _, dir := range s.rootTree(rt).dirs {
		_ = s.watcher.Add(dir)
	}
}

func (s *server) watched(name string) bool {
	if !isMarkdown(name) {
		return false
	}
	s.rootsMu.RLock()
	defer s.rootsMu.RUnlock()
	return slices.ContainsFunc(s.roots, func(rt *root) bool { return rt.covers(name) })
}

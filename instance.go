package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

type instance struct {
	port  string
	token string
}

var talk = &http.Client{Timeout: 2 * time.Second}

func (in instance) origin() string {
	return "http://127.0.0.1:" + in.port
}

func instancesDir() string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "mds", "instances")
}

func readInstances() []instance {
	dir := instancesDir()
	if dir == "" {
		return nil
	}
	_ = os.Remove(filepath.Join(filepath.Dir(dir), "instances.json"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	running := []instance{}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		token, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		running = append(running, instance{port: entry.Name(), token: string(token)})
	}
	return running
}

func addInstance(port, token string) {
	dir := instancesDir()
	if dir == "" || os.MkdirAll(dir, 0o700) != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, port), []byte(token), 0o600)
}

func dropInstance(port string) {
	if dir := instancesDir(); dir != "" {
		_ = os.Remove(filepath.Join(dir, port))
	}
}

func call(running instance, path string) (*http.Response, error) {
	request, err := http.NewRequest(http.MethodPost, running.origin()+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(tokenHeader, running.token)
	return talk.Do(request)
}

func postAdd(running instance, target string) (page string, alive, added bool) {
	response, err := call(running, "/_add?path="+url.QueryEscape(target))
	if err != nil {
		return "", !errors.Is(err, syscall.ECONNREFUSED), false
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		return "", true, false
	}
	return string(body), true, true
}

func addToRunning(target string) (string, bool) {
	for _, running := range readInstances() {
		page, alive, added := postAdd(running, target)
		if !alive {
			dropInstance(running.port)
			continue
		}
		if added {
			return page, true
		}
	}
	return "", false
}

func stopRunning() {
	stopped := 0
	for _, running := range readInstances() {
		if _, err := call(running, "/_stop"); err == nil {
			stopped++
		}
		dropInstance(running.port)
	}
	if stopped == 0 {
		fmt.Println("mds: nothing to stop")
		return
	}
	fmt.Printf("mds: stopped %d server(s)\n", stopped)
}

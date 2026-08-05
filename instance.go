package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"
)

type instance struct {
	URL string `json:"url"`
	Pid int    `json:"pid"`
}

var talk = &http.Client{Timeout: 500 * time.Millisecond}

func instancesFile() string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "mds", "instances.json")
}

func readInstances() []instance {
	var running []instance
	body, err := os.ReadFile(instancesFile())
	if err != nil || json.Unmarshal(body, &running) != nil {
		return nil
	}
	return running
}

func writeInstances(running []instance) {
	path := instancesFile()
	body, err := json.Marshal(running)
	if path == "" || err != nil || os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	_ = os.WriteFile(path, body, 0o644)
}

func addInstance(serverURL string) {
	writeInstances(append(readInstances(), instance{URL: serverURL, Pid: os.Getpid()}))
}

func dropInstance(serverURL string) {
	writeInstances(slices.DeleteFunc(readInstances(), func(running instance) bool {
		return running.URL == serverURL
	}))
}

func postAdd(serverURL, target string) (page string, alive, added bool) {
	response, err := talk.Post(serverURL+"/_add?path="+url.QueryEscape(target), "", nil)
	if err != nil {
		return "", false, false
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		return "", true, false
	}
	return string(body), true, true
}

func addToRunning(target string) (string, bool) {
	alive, page, found := []instance{}, "", false
	for _, running := range readInstances() {
		if found {
			alive = append(alive, running)
			continue
		}
		body, answered, added := postAdd(running.URL, target)
		if !answered {
			continue
		}
		alive = append(alive, running)
		page, found = body, added
	}
	writeInstances(alive)
	return page, found
}

func stopRunning() {
	stopped := 0
	for _, running := range readInstances() {
		if _, err := talk.Post(running.URL+"/_stop", "", nil); err == nil {
			stopped++
		}
	}
	writeInstances(nil)
	if stopped == 0 {
		fmt.Println("mds: nothing to stop")
		return
	}
	fmt.Printf("mds: stopped %d server(s)\n", stopped)
}

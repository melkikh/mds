package main

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDetectAgent(t *testing.T) {
	for _, name := range agentEnv {
		t.Setenv(name, "")
	}
	if got := detectAgent(); got != "" {
		t.Errorf("detectAgent() = %q with no agent env, want empty", got)
	}
	t.Setenv("CLAUDECODE", "1")
	if got := detectAgent(); got != "CLAUDECODE" {
		t.Errorf("detectAgent() = %q, want CLAUDECODE", got)
	}
}

func TestSkillAsset(t *testing.T) {
	skill, err := assetFS.ReadFile("assets/skill.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"---\nname: mds\n", "description:", "mds <file.md>"} {
		if !strings.Contains(string(skill), want) {
			t.Errorf("skill.md missing %q", want)
		}
	}
}

func TestAgentHintsDoNotCreateASecondTab(t *testing.T) {
	open := captureStdout(t, func() { announce("http://example.test/new", "CODEX", 0, options{noOpen: true}) })
	if !strings.Contains(open, "browser to open") || !strings.Contains(open, "only show the url") {
		t.Errorf("hint with no tab = %q, so an agent cannot tell whether it should share the url", open)
	}
	reused := captureStdout(t, func() { announce("http://example.test/reused", "CODEX", 1, options{noOpen: true}) })
	if !strings.Contains(reused, "moved an open tab") || !strings.Contains(reused, "do not repeat the url") {
		t.Errorf("hint with an open tab = %q, so an agent may make the user open a duplicate", reused)
	}
}

func TestDetachedServer(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	binary := filepath.Join(t.TempDir(), "mds")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	launcher := exec.Command(binary, "--no-open", fixture(t))
	cache := t.TempDir()
	launcher.Env = append(os.Environ(), "CLAUDECODE=1", "MDS_PORT=0", "HOME="+cache,
		"XDG_CACHE_HOME="+cache, "LOCALAPPDATA="+cache)
	stdout, err := launcher.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := launcher.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := launcher.Start(); err != nil {
		t.Fatal(err)
	}
	printed, _ := io.ReadAll(stdout)
	if process, err := os.FindProcess(agentHintPid(t, string(printed))); err == nil {
		t.Cleanup(func() { _ = process.Kill() })
	}
	drained := make(chan []byte, 1)
	go func() {
		leftover, _ := io.ReadAll(stderr)
		drained <- leftover
	}()
	select {
	case leftover := <-drained:
		if len(leftover) > 0 {
			t.Errorf("mds wrote to stderr: %s", leftover)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stderr never reached EOF: the detached server still holds the pipe it inherited, " +
			"so an agent reading stderr to the end would block for as long as the server runs")
	}
	if err := launcher.Wait(); err != nil {
		t.Fatalf("mds did not exit on its own: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(printed)), "\n")
	home, fragment, _ := strings.Cut(lines[0], "#")
	key := strings.TrimPrefix(fragment, tokenParam+"=")
	if !strings.HasPrefix(home, "http://127.0.0.1:") {
		t.Fatalf("first stdout line = %q, want a URL", lines[0])
	}
	if key == "" || key == fragment {
		t.Fatalf("printed url = %q, want it to carry the key in the fragment", lines[0])
	}
	if !strings.Contains(string(printed), "does not block") {
		t.Errorf("agent hint missing from stdout:\n%s", printed)
	}
	page := home + "/docs/intro.md"
	locked, err := http.Get(page)
	if err != nil {
		t.Fatalf("detached server unreachable: %v", err)
	}
	locked.Body.Close()
	if locked.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET %s without the key = %d, want 401", page, locked.StatusCode)
	}
	// walk in the way a browser does: hand the key to /_auth, keep the cookie it gives back
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	browser := &http.Client{Jar: jar}
	origin, err := url.Parse(home)
	if err != nil {
		t.Fatal(err)
	}
	origin.Path = "/_auth"
	unlocked, err := browser.Post(origin.String(), "text/plain", strings.NewReader(key))
	if err != nil {
		t.Fatalf("detached server unreachable: %v", err)
	}
	unlocked.Body.Close()
	if unlocked.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /_auth with the printed key = %d, want 204", unlocked.StatusCode)
	}
	response, err := browser.Get(page)
	if err != nil {
		t.Fatalf("detached server unreachable: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "Intro") {
		t.Error("detached server did not render the file")
	}
}

func agentHintPid(t *testing.T, hint string) int {
	t.Helper()
	match := regexp.MustCompile(`pid (\d+)`).FindStringSubmatch(hint)
	if match == nil {
		t.Fatalf("no pid in the agent hint:\n%s", hint)
	}
	pid, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

package main

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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

func TestChildArgs(t *testing.T) {
	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"-b", "plan.md"}, []string{"plan.md"}},
		{[]string{"docs", "--background", "-d", "2", "--no-open"}, []string{"docs", "-d", "2", "--no-open"}},
		{[]string{"plan.md"}, []string{"plan.md"}},
		{nil, []string{}},
	} {
		if got := childArgs(c.args); !slices.Equal(got, c.want) {
			t.Errorf("childArgs(%q) = %q, want %q", c.args, got, c.want)
		}
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

func TestDetachedServer(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	binary := filepath.Join(t.TempDir(), "mds")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	launcher := exec.Command(binary, "-b", "--new", "--no-open", fixture(t))
	cache := t.TempDir()
	launcher.Env = append(os.Environ(), "CLAUDECODE=1", "HOME="+cache, "XDG_CACHE_HOME="+cache, "LOCALAPPDATA="+cache)
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
	drained := make(chan []byte, 1)
	go func() {
		leftover, _ := io.ReadAll(stderr)
		drained <- leftover
	}()
	select {
	case leftover := <-drained:
		if len(leftover) > 0 {
			t.Errorf("mds -b wrote to stderr: %s", leftover)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stderr never reached EOF: the detached server still holds the pipe it inherited, " +
			"so an agent reading stderr to the end would block for as long as the server runs")
	}
	if err := launcher.Wait(); err != nil {
		t.Fatalf("mds -b did not exit: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(printed)), "\n")
	url := lines[0]
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("first stdout line = %q, want a URL", url)
	}
	if !strings.Contains(string(printed), "does not block") {
		t.Errorf("agent hint missing from stdout:\n%s", printed)
	}
	pid := agentHintPid(t, string(printed))
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Kill()

	response, err := http.Get(url + "/docs/intro.md")
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

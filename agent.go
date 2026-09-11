package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

const childEnv = "MDS_CHILD"

const reuseHint = `mds moved an open tab to that path and added it to the sidebar.
do not repeat the url unless the user says the tab did not move. the page live-reloads on save.
`

const openHint = `mds asked the browser to open that path.
only show the url if the browser did not open. the page live-reloads on save.
`

var agentEnv = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CODEX_SANDBOX", "OPENCODE_BIN_PATH", "CURSOR_TRACE_ID", "AIDER_MODEL"}

func detectAgent() string {
	for _, name := range agentEnv {
		if os.Getenv(name) != "" {
			return name
		}
	}
	return ""
}

func detach(agent string) {
	binary, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	child := exec.Command(binary, os.Args[1:]...)
	child.Env = append(os.Environ(), childEnv+"=1")
	child.SysProcAttr = detachAttr()
	stdout, err := child.StdoutPipe()
	if err != nil {
		fatal(err)
	}
	stderr, err := child.StderrPipe()
	if err != nil {
		fatal(err)
	}
	if err := child.Start(); err != nil {
		fatal(err)
	}
	url, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		reason, _ := io.ReadAll(stderr)
		fatal(fmt.Errorf("background server failed to start: %s", strings.TrimSpace(string(reason))))
	}
	fmt.Print(url)
	if agent == "" {
		fmt.Fprintf(os.Stderr, "mds: serving in the background, pid %d\n", child.Process.Pid)
		return
	}
	fmt.Printf(`mds serves that path in the background (pid %d) and does not block.
the page live-reloads on every save, so there is no need to restart it.
mds asked the browser to open it. only show the url if the browser did not open.
another file: run mds again with its path, it lands in the sidebar of the same page.
stop it: mds --stop
`, child.Process.Pid)
}

func printSkill() {
	os.Stdout.Write(skillFile)
}

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

const childEnv = "MDS_CHILD"

var agentEnv = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CODEX_SANDBOX", "OPENCODE_BIN_PATH", "CURSOR_TRACE_ID", "AIDER_MODEL"}

func detectAgent() string {
	for _, name := range agentEnv {
		if os.Getenv(name) != "" {
			return name
		}
	}
	return ""
}

func childArgs(args []string) []string {
	kept := []string{}
	for _, arg := range args {
		if arg != "-b" && arg != "--background" {
			kept = append(kept, arg)
		}
	}
	return kept
}

func detach(agent string) {
	binary, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	child := exec.Command(binary, childArgs(os.Args[1:])...)
	child.Env = append(os.Environ(), childEnv+"=1")
	child.Stderr = os.Stderr
	child.SysProcAttr = detachAttr()
	stdout, err := child.StdoutPipe()
	if err != nil {
		fatal(err)
	}
	if err := child.Start(); err != nil {
		fatal(err)
	}
	url, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		fatal(errors.New("background server failed to start"))
	}
	fmt.Print(url)
	if agent == "" {
		fmt.Fprintf(os.Stderr, "mds: serving in the background, pid %d\n", child.Process.Pid)
		return
	}
	fmt.Printf(`mds serves that file in the background (pid %d) and does not block.
The page live-reloads on every save, so there is no need to restart it.
Show the URL above to the user instead of pasting the file into the chat.
Another file: run mds again with its path. Stop it: kill %d
Install the slash command: mds --skill > ~/.claude/skills/mds/SKILL.md
`, child.Process.Pid, child.Process.Pid)
}

func printSkill() {
	skill, err := assetFS.ReadFile("assets/skill.md")
	if err != nil {
		fatal(err)
	}
	os.Stdout.Write(skill)
}

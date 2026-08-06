package main

import (
	"embed"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

//go:embed assets
var assetFS embed.FS

//go:embed assets/skill.md
var skillFile []byte

const usage = `mds — render markdown in the browser

usage:
  mds [path] [flags]

  path               file or directory to serve (default ".")
  -d, --depth N      max directory depth, 0 = root only, -1 = unlimited (default 5)
  -s, --skip NAMES   comma-separated directory names to skip (default "node_modules,vendor,dist,build,target")
  -b, --background   serve in a detached process, print the URL and exit
      --new          start a separate server instead of reusing the running one
      --no-open      do not open a browser, just print the URL
      --stop         stop every running server
      --skill        print a skill file teaching a coding agent to use mds;
                     redirect it into wherever your agent keeps its instructions
  -h, --help         show this help

  MDS_EDITOR         command the pencil button runs, e.g. "code -g" (default: system opener)
                     quote a path that has spaces: "\"C:\\Program Files\\ed.exe\" -g"

A second mds adds its path to the server that is already running and prints the URL of
that page; the sidebar of the open tab picks it up. Running under a coding agent
(Claude Code, Codex, ...) implies --background.
`

const sharedPort = "8080"

var defaultSkip = []string{"node_modules", "vendor", "dist", "build", "target"}

type options struct {
	target     string
	depth      int
	skip       []string
	background bool
	fresh      bool
	noOpen     bool
}

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fatal(err)
	}
	abs, err := filepath.Abs(opts.target)
	if err != nil {
		fatal(err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		fatal(err)
	}
	agent := detectAgent()
	if !opts.fresh {
		if page, ok := addToRunning(abs); ok {
			announce(page, agent)
			return
		}
	}
	if os.Getenv(childEnv) == "" && (opts.background || agent != "") {
		detach(agent)
		return
	}
	listener, port, claimed := listen()
	if !claimed && !opts.fresh {
		if page, ok := joinHolder(abs); ok {
			_ = listener.Close()
			announce(page, agent)
			return
		}
		fmt.Fprintf(os.Stderr, "mds: port %s is taken by something else, serving on %s\n", sharedPort, port)
	}
	s := newServer(newRoot(abs, info.IsDir(), ""), opts)
	httpServer := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	s.port, s.shutdown = port, func() { _ = httpServer.Close() }
	addInstance(port, s.token)
	fmt.Println(s.origin())
	if !opts.noOpen {
		_ = openExternal(s.origin())
	}
	go s.watch()
	if err := httpServer.Serve(listener); err != http.ErrServerClosed {
		fatal(err)
	}
	dropInstance(port)
}

func announce(page, agent string) {
	fmt.Println(page)
	if agent != "" {
		fmt.Print(reuseHint)
	}
}

func joinHolder(target string) (string, bool) {
	for attempt := range 5 {
		if attempt > 0 {
			time.Sleep(20 * time.Millisecond)
		}
		if page, ok := addToRunning(target); ok {
			return page, true
		}
	}
	return "", false
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "mds:", err)
	os.Exit(1)
}

func parseArgs(args []string) (options, error) {
	opts := options{target: ".", depth: 5, skip: defaultSkip}
	for i := 0; i < len(args); i++ {
		value := ""
		if i+1 < len(args) {
			value = args[i+1]
		}
		switch args[i] {
		case "-h", "--help":
			fmt.Print(usage)
			os.Exit(0)
		case "--skill":
			printSkill()
			os.Exit(0)
		case "--stop":
			stopRunning()
			os.Exit(0)
		case "-b", "--background":
			opts.background = true
		case "--new":
			opts.fresh = true
		case "--no-open":
			opts.noOpen = true
		case "-d", "--depth":
			if n, err := strconv.Atoi(value); err == nil {
				opts.depth = n
			}
			i++
		case "-s", "--skip":
			opts.skip = strings.Split(value, ",")
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("unknown flag %q, see mds --help", args[i])
			}
			opts.target = args[i]
		}
	}
	return opts, nil
}

func listen() (net.Listener, string, bool) {
	if l, err := net.Listen("tcp", "127.0.0.1:"+sharedPort); err == nil {
		return l, sharedPort, true
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal(err)
	}
	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		fatal(err)
	}
	return l, port, false
}

func openExternal(target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}

func openInEditor(target string) error {
	editor := splitCommand(os.Getenv("MDS_EDITOR"))
	if len(editor) == 0 {
		return openExternal(target)
	}
	return exec.Command(editor[0], append(editor[1:], target)...).Start()
}

func splitCommand(command string) []string {
	var flat strings.Builder
	quoted := false
	for _, r := range command {
		switch {
		case r == '"':
			quoted = !quoted
		case !quoted && unicode.IsSpace(r):
			flat.WriteRune(0)
		default:
			flat.WriteRune(r)
		}
	}
	return slices.DeleteFunc(strings.Split(flat.String(), "\x00"),
		func(part string) bool { return part == "" })
}

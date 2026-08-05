package main

import (
	"embed"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

//go:embed assets
var assetFS embed.FS

const usage = `mds — render markdown in the browser

usage:
  mds [path] [flags]

  path               file or directory to serve (default ".")
  -d, --depth N      max directory depth, 0 = root only, -1 = unlimited (default 5)
  -t, --theme NAME   initial theme, light or dark (default "light")
  -s, --skip NAMES   comma-separated directory names to skip (default "node_modules,vendor,dist,build,target")
  -b, --background   serve in a detached process, print the URL and exit
      --new          start a separate server instead of reusing the running one
      --no-open      do not open a browser, just print the URL
      --stop         stop every running server
      --skill        print a skill file teaching a coding agent to use mds
  -h, --help         show this help

  MDS_EDITOR         command the pencil button runs, e.g. "code -g" (default: system opener)

A second mds adds its path to the server that is already running and prints the URL of
that page; the sidebar of the open tab picks it up. Running under a coding agent
(Claude Code, Codex, ...) implies --background.
`

var defaultSkip = []string{"node_modules", "vendor", "dist", "build", "target"}

type options struct {
	target     string
	depth      int
	theme      string
	skip       []string
	background bool
	fresh      bool
	noOpen     bool
}

func main() {
	opts := parseArgs(os.Args[1:])
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
			fmt.Println(page)
			if agent != "" {
				fmt.Print(reuseHint)
			}
			return
		}
	}
	if os.Getenv(childEnv) == "" && (opts.background || agent != "") {
		detach(agent)
		return
	}
	s := &server{
		roots:  []*root{newRoot(abs, info.IsDir(), "")},
		depth:  opts.depth,
		theme:  opts.theme,
		skip:   opts.skip,
		launch: openInEditor,
		tmpl:   template.Must(template.ParseFS(assetFS, "assets/page.html")),
		subs:   map[chan struct{}]struct{}{},
	}
	listener, serverURL := listen()
	httpServer := &http.Server{Handler: s.routes()}
	s.url, s.shutdown = serverURL, func() { _ = httpServer.Close() }
	fmt.Println(serverURL)
	if !opts.noOpen {
		_ = openExternal(serverURL)
	}
	addInstance(serverURL)
	go s.watch()
	if err := httpServer.Serve(listener); err != http.ErrServerClosed {
		fatal(err)
	}
	dropInstance(serverURL)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "mds:", err)
	os.Exit(1)
}

func parseArgs(args []string) options {
	opts := options{target: ".", depth: 5, theme: "light", skip: defaultSkip}
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
		case "-t", "--theme":
			if value == "dark" {
				opts.theme = "dark"
			}
			i++
		case "-s", "--skip":
			opts.skip = strings.Split(value, ",")
			i++
		default:
			opts.target = args[i]
		}
	}
	return opts
}

func listen() (net.Listener, string) {
	if l, err := net.Listen("tcp", "127.0.0.1:8080"); err == nil {
		return l, "http://127.0.0.1:8080"
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal(err)
	}
	return l, fmt.Sprintf("http://127.0.0.1:%d", l.Addr().(*net.TCPAddr).Port)
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
	editor := strings.Fields(os.Getenv("MDS_EDITOR"))
	if len(editor) == 0 {
		return openExternal(target)
	}
	return exec.Command(editor[0], append(editor[1:], target)...).Start()
}

package main

import (
	"embed"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

//go:embed assets
var assetFS embed.FS

//go:embed assets/skill.md
var skillFile []byte

const usage = `mds — render markdown in the browser

usage: mds [path] [flags]

  path               file or directory (default ".")
  -d, --depth N      scan depth: 0 root, -1 unlimited (default 5)
  -s, --skip NAMES   comma-separated dirs to skip
                     (default node_modules,vendor,dist,build,target)
  -f, --foreground   stay in this terminal
      --new          use a separate server
      --no-open      print the url without opening it
      --service ACT  install, remove, status, restart, or run
      --stop         stop all mds servers
      --skill        print agent instructions
  -h, --help         show help

  MDS_PORT           port (default ` + defaultPort + `)
  MDS_EDITOR         pencil command, e.g. "code -g"
  MDS_REMOTE_IMAGES  allow off-host images when true (default false)

mds detaches, opens the page, and reuses a running server on later runs.
use --service install to start one at login. open the printed url whole: it has the key.
`

const (
	defaultPort  = "6337"
	defaultDepth = 5
	portEnv      = "MDS_PORT"
	editorEnv    = "MDS_EDITOR"
)

func sharedPort() string {
	if port := os.Getenv(portEnv); port != "" {
		return port
	}
	return defaultPort
}

var defaultSkip = []string{"node_modules", "vendor", "dist", "build", "target"}

type options struct {
	target     string
	depth      int
	skip       []string
	foreground bool
	fresh      bool
	noOpen     bool
	service    string
}

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fatal(err)
	}
	if opts.service != "" {
		if opts.service != serviceRun {
			if err := runService(opts); err != nil {
				fatal(err)
			}
			return
		}
		if os.Getenv(childEnv) == "" && !opts.foreground {
			detach("")
			return
		}
		serve(nil, opts)
		return
	}
	abs, err := filepath.Abs(opts.target)
	if err != nil {
		fatal(err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		fatal(err)
	}
	if !opts.fresh {
		if page, tabs, ok := addToRunning(abs); ok {
			announce(page, detectAgent(), tabs, opts)
			return
		}
	}
	if os.Getenv(childEnv) == "" && !opts.foreground {
		detach(detectAgent())
		return
	}
	serve(newRoot(abs, info.IsDir()), opts)
}

// serve is the server itself: first is the path it opens with, or nothing at all when this
// is the login server waiting for one.
func serve(first *root, opts options) {
	listener, port, claimed := listen()
	if !claimed {
		switch {
		case first == nil:
			if holdsPort() {
				_ = listener.Close()
				fmt.Fprintf(os.Stderr, "mds: port %s already has an mds on it\n", sharedPort())
				return
			}
			fmt.Fprintf(os.Stderr, "mds: port %s is taken by something else, serving on %s\n", sharedPort(), port)
		case !opts.fresh:
			if page, tabs, ok := joinHolder(first.target()); ok {
				_ = listener.Close()
				announce(page, detectAgent(), tabs, opts)
				return
			}
			fmt.Fprintf(os.Stderr, "mds: port %s is taken by something else, serving on %s\n", sharedPort(), port)
		}
	}
	s := newServer(first, opts)
	httpServer := &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	s.port, s.shutdown = port, func() { _ = httpServer.Close() }
	addInstance(port, s.token)
	onSignal(s.shutdown)
	entrance := s.link(s.home())
	fmt.Println(entrance)
	if !opts.noOpen {
		_ = openExternal(entrance)
	}
	go s.watch()
	if err := httpServer.Serve(listener); err != http.ErrServerClosed {
		fatal(err)
	}
	dropInstance(port)
}

// onSignal closes the server when the system asks the process to go away. launchd and
// systemd end a login server that way at every logout, and an entry in the instances file
// must not outlive the port it names.
func onSignal(shutdown func()) chan<- os.Signal {
	asked := make(chan os.Signal, 1)
	signal.Notify(asked, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-asked
		shutdown()
	}()
	return asked
}

func announce(page, agent string, tabs int, opts options) {
	fmt.Println(page)
	if tabs == 0 && !opts.noOpen {
		_ = openExternal(page)
	}
	if agent != "" {
		fmt.Print(reuseHint)
	}
}

func joinHolder(target string) (string, int, bool) {
	for attempt := range 5 {
		if attempt > 0 {
			time.Sleep(20 * time.Millisecond)
		}
		if page, tabs, ok := addToRunning(target); ok {
			return page, tabs, true
		}
	}
	return "", 0, false
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "mds:", err)
	os.Exit(1)
}

func parseArgs(args []string) (options, error) {
	opts := options{target: ".", depth: defaultDepth, skip: defaultSkip}
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
			// detaching is the default now, the flag stays so old habits keep working
		case "-f", "--foreground":
			opts.foreground = true
		case "--new":
			opts.fresh = true
		case "--no-open":
			opts.noOpen = true
		case "--service":
			if !slices.Contains(serviceActions, value) {
				return opts, fmt.Errorf("--service takes one of %s, not %q", spokenList(serviceActions), value)
			}
			opts.service = value
			// nobody is at the keyboard when a login server starts
			opts.noOpen = opts.noOpen || value == serviceRun
			i++
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
	shared := sharedPort()
	if l, err := net.Listen("tcp", "127.0.0.1:"+shared); err == nil {
		return l, shared, true
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

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// serviceLabel names the login item on every system: the launchd label, the systemd unit
// and the registry value all read "mds".
const serviceLabel = "mds"

// serviceRun is what the login item itself runs. The server it starts holds the port and
// nothing else, so the first mds <path> of the day has a page to join instead of one to
// wait for.
const serviceRun = "run"

var serviceActions = []string{"install", "remove", "status", "restart", serviceRun}

var errNoService = errors.New("mds does not know how to start itself at login here")

// serviceSettings are what a login server cannot pick up from a shell it never had, so
// install writes them into the unit as they are set right now.
var serviceSettings = []string{portEnv, editorEnv, remoteEnv}

// unit is everything the login item needs to say: which binary, with which arguments, and
// under which settings.
type unit struct {
	exe  string
	args []string
	env  []string
}

func serviceUnit(opts options) (unit, error) {
	exe, err := os.Executable()
	if err != nil {
		return unit{}, err
	}
	// the login item outlives the symlink it was installed through
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	u := unit{exe: exe}
	if serviceForeground {
		u.args = append(u.args, "--foreground")
	}
	u.args = append(u.args, "--service", serviceRun)
	if opts.depth != defaultDepth {
		u.args = append(u.args, "--depth", strconv.Itoa(opts.depth))
	}
	if !slices.Equal(opts.skip, defaultSkip) {
		u.args = append(u.args, "--skip", strings.Join(opts.skip, ","))
	}
	for _, name := range serviceSettings {
		// a newline in a value would end the line it is written on and start something else
		if value := os.Getenv(name); value != "" && !strings.ContainsAny(value, "\r\n") {
			u.env = append(u.env, name+"="+value)
		}
	}
	return u, nil
}

func (u unit) argv() []string {
	return append([]string{u.exe}, u.args...)
}

func (u unit) command() string {
	spoken := []string{}
	for _, part := range u.argv() {
		if strings.ContainsAny(part, ` "`) {
			part = strconv.Quote(part)
		}
		spoken = append(spoken, part)
	}
	return strings.Join(spoken, " ")
}

func runService(opts options) error {
	u, err := serviceUnit(opts)
	if err != nil {
		return err
	}
	switch opts.service {
	case "install":
		if opts.target != "." {
			return errors.New("--service install takes no path: the login server starts empty, " +
				"and mds <path> mounts into it")
		}
		if err := serviceInstall(u); err != nil {
			return err
		}
		where, err := serviceLocation()
		if err != nil {
			return err
		}
		fmt.Println("mds starts at login now:", where)
		if len(u.env) > 0 {
			fmt.Println("it carries", spokenList(names(u.env)), "as they are set here")
		}
		return nil
	case "remove":
		if installed, _ := serviceState(u); !installed {
			fmt.Println("mds: nothing to remove, mds does not start at login")
			return nil
		}
		if err := serviceRemove(); err != nil {
			return err
		}
		fmt.Println("mds no longer starts at login")
		return nil
	case "restart":
		if installed, _ := serviceState(u); !installed {
			return errors.New("mds does not start at login yet, see mds --service install")
		}
		if err := serviceRestart(u); err != nil {
			return err
		}
		fmt.Println("mds: restarted")
		return nil
	case "status":
		printState(u)
		return nil
	}
	return fmt.Errorf("unknown --service action %q, one of %s", opts.service, spokenList(serviceActions))
}

func printState(u unit) {
	say := func(name, value string) { fmt.Printf("%-10s %s\n", name, value) }
	where, err := serviceLocation()
	if err != nil {
		say("autostart", "not on this system")
		say("by hand", u.command())
	} else {
		installed, ours := serviceState(u)
		switch {
		case !installed:
			say("autostart", "off")
		case !ours:
			say("autostart", "on, but pointing at another mds — mds --service install moves it here")
		default:
			say("autostart", "on")
		}
		say("item", where)
		say("runs", u.command())
	}
	live := liveInstances()
	if len(live) == 0 {
		say("serving", "nothing")
		return
	}
	for i, running := range live {
		name := "serving"
		if i > 0 {
			name = ""
		}
		say(name, running.origin()+"/#"+tokenParam+"="+running.token)
	}
}

// liveInstances is the instances file with the entries nothing answers on taken out.
func liveInstances() []instance {
	live := []instance{}
	for _, running := range readInstances() {
		if !alive(running) {
			dropInstance(running.port)
			continue
		}
		live = append(live, running)
	}
	return live
}

// holdsPort tells whether an mds already answers on the shared port. A login server has no
// path to hand over, so when one is there this one has nothing left to do.
func holdsPort() bool {
	for _, running := range readInstances() {
		if running.port != sharedPort() {
			continue
		}
		if alive(running) {
			return true
		}
		dropInstance(running.port)
	}
	return false
}

func names(env []string) []string {
	named := []string{}
	for _, value := range env {
		name, _, _ := strings.Cut(value, "=")
		named = append(named, name)
	}
	return named
}

func spokenList(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

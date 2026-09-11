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
	if opts.target != "." {
		return fmt.Errorf("--%s takes no path", opts.service)
	}
	switch opts.service {
	case "install":
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
		if !serviceInstalled() {
			stopped := stopInstances()
			if err := dropServiceSessions(); err != nil {
				return err
			}
			if stopped > 0 {
				fmt.Println("mds stopped; it did not start at login")
				return nil
			}
			fmt.Println("mds: nothing to remove, mds does not start at login")
			return nil
		}
		if err := serviceRemove(); err != nil {
			return err
		}
		stopInstances()
		if err := dropServiceSessions(); err != nil {
			return err
		}
		fmt.Println("mds no longer starts at login")
		return nil
	}
	return fmt.Errorf("unknown service action %q", opts.service)
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

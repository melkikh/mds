package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func testUnit(t *testing.T, opts options) unit {
	t.Helper()
	t.Setenv(portEnv, "")
	t.Setenv(editorEnv, "")
	t.Setenv(remoteEnv, "")
	u, err := serviceUnit(opts)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func defaultOptions() options {
	return options{target: ".", depth: defaultDepth, skip: defaultSkip}
}

func TestServiceUnitRunsTheLoginServer(t *testing.T) {
	u := testUnit(t, defaultOptions())
	if !slices.Contains(u.args, "--service") || !slices.Contains(u.args, serviceRun) {
		t.Errorf("login item args = %q, want them to run --service run, or nothing starts at login", u.args)
	}
	if slices.Contains(u.args, "--foreground") != serviceForeground {
		t.Errorf("login item args = %q, want --foreground: %v, or the service manager loses the process",
			u.args, serviceForeground)
	}
	if slices.Contains(u.args, "--depth") || slices.Contains(u.args, "--skip") {
		t.Errorf("login item args = %q, want defaults left out of the unit", u.args)
	}
	if filepath.Base(u.exe) == "" || !filepath.IsAbs(u.exe) {
		t.Errorf("login item runs %q, want an absolute path, or logon has nothing to find", u.exe)
	}
}

func TestServiceUnitKeepsTheSettingsRootsWillBeScannedWith(t *testing.T) {
	opts := defaultOptions()
	opts.depth, opts.skip = 2, []string{"out"}
	u := testUnit(t, opts)
	command := u.command()
	for _, want := range []string{"--depth 2", "--skip out"} {
		if !strings.Contains(command, want) {
			t.Errorf("login item runs %q, want %q in it, or paths added later are scanned another way",
				command, want)
		}
	}
}

func TestServiceUnitCarriesSettingsAShellWouldHave(t *testing.T) {
	u := testUnit(t, defaultOptions())
	if len(u.env) != 0 {
		t.Errorf("login item carries %q with nothing set, want nothing", u.env)
	}
	t.Setenv(portEnv, "9001")
	t.Setenv(editorEnv, "code -g")
	u, err := serviceUnit(defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{portEnv + "=9001", editorEnv + "=code -g"}
	if !slices.Equal(u.env, want) {
		t.Errorf("login item carries %q, want %q: it never sees the shell these were set in", u.env, want)
	}
}

func TestServiceUnitDropsSettingsThatWouldSplitTheUnit(t *testing.T) {
	testUnit(t, defaultOptions())
	t.Setenv(editorEnv, "code\nExecStart=/bin/sh")
	u, err := serviceUnit(defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(u.env) != 0 {
		t.Errorf("login item carries %q, want a value with a newline in it left out, "+
			"or it writes a line of its own into the unit", u.env)
	}
}

func TestServiceUnitQuotesWhatItPrints(t *testing.T) {
	u := unit{exe: "/Applications/My Tools/mds", args: []string{"--service", "run"}}
	if got, want := u.command(), `"/Applications/My Tools/mds" --service run`; got != want {
		t.Errorf("command() = %s, want %s, or the path reads as two words", got, want)
	}
}

func TestParseArgsService(t *testing.T) {
	for _, action := range []string{"install", "remove"} {
		opts, err := parseArgs([]string{"--" + action})
		if err != nil {
			t.Errorf("parseArgs(--%s) = %v, want it accepted", action, err)
			continue
		}
		if opts.service != action {
			t.Errorf("parseArgs(--%s) service = %q, want %q", action, opts.service, action)
		}
	}
	if opts, _ := parseArgs([]string{"--service", serviceRun}); !opts.noOpen {
		t.Error("--service run opens a browser, but nobody is at the keyboard when a login server starts")
	}
	for _, args := range [][]string{{"--service"}, {"--service", "install"}, {"--service", "status"}} {
		opts, err := parseArgs(args)
		if err == nil {
			t.Errorf("parseArgs(%q) = %+v, so internal service controls remain public", args, opts)
			continue
		}
		if !strings.Contains(err.Error(), "internal") {
			t.Errorf("parseArgs(%q) = %v, want the message to point away from internal controls", args, err)
		}
	}
}

func TestServiceInstallRefusesAPath(t *testing.T) {
	opts := defaultOptions()
	opts.target, opts.service = "docs", "install"
	err := runService(opts)
	if err == nil {
		t.Fatal("mds docs --install installed something, so the path was silently ignored")
	}
	if !strings.Contains(err.Error(), "takes no path") {
		t.Errorf("error = %v, want it to say the control does not accept a path", err)
	}
}

func TestSpokenList(t *testing.T) {
	for _, c := range []struct {
		words []string
		want  string
	}{
		{nil, ""},
		{[]string{"one"}, "one"},
		{[]string{"one", "two"}, "one and two"},
		{[]string{"one", "two", "three"}, "one, two and three"},
	} {
		if got := spokenList(c.words); got != c.want {
			t.Errorf("spokenList(%q) = %q, want %q", c.words, got, c.want)
		}
	}
}

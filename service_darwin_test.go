package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceContentIsALaunchdJob(t *testing.T) {
	u := unit{exe: "/opt/mds & co/mds", args: []string{"--foreground", "--service", "run"},
		env: []string{editorEnv + "=code -g"}}
	job := serviceContent(u)
	for _, want := range []string{
		"<key>Label</key>\n  <string>mds</string>",
		"<string>/opt/mds &amp; co/mds</string>",
		"<string>--foreground</string>",
		"<string>--service</string>",
		"<string>run</string>",
		"<key>RunAtLoad</key>",
		"<key>" + editorEnv + "</key>\n    <string>code -g</string>",
	} {
		if !strings.Contains(job, want) {
			t.Errorf("launchd job is missing %q:\n%s", want, job)
		}
	}
	if strings.Contains(job, "KeepAlive") {
		t.Errorf("launchd job asks for KeepAlive, so launchd would undo mds --stop within seconds:\n%s", job)
	}
}

func TestServiceStateSeesWhichMdsIsInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	u := unit{exe: "/opt/mds/mds", args: []string{"--service", "run"}}
	if installed, _ := serviceState(u); installed {
		t.Fatal("serviceState found a login item in an empty home")
	}
	path, err := serviceLocation()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(serviceContent(u)), 0o644); err != nil {
		t.Fatal(err)
	}
	if installed, ours := serviceState(u); !installed || !ours {
		t.Errorf("serviceState = %v, %v for the job it just wrote, want true, true", installed, ours)
	}
	moved := unit{exe: "/usr/local/bin/mds", args: u.args}
	if installed, ours := serviceState(moved); !installed || ours {
		t.Errorf("serviceState = %v, %v for another binary, want true, false, "+
			"so status can say the login item points elsewhere", installed, ours)
	}
}

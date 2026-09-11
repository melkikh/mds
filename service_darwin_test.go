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

func TestServiceInstalledSeesTheLoginItem(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	u := unit{exe: "/opt/mds/mds", args: []string{"--service", "run"}}
	if serviceInstalled() {
		t.Fatal("serviceInstalled found a login item in an empty home")
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
	if !serviceInstalled() {
		t.Error("serviceInstalled missed the login item, so --remove would leave it behind")
	}
}

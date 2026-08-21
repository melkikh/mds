package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceContentIsASystemdUnit(t *testing.T) {
	u := unit{exe: "/opt/my tools/mds", args: []string{"--foreground", "--service", "run"},
		env: []string{editorEnv + "=code -g"}}
	unitFile := serviceContent(u)
	for _, want := range []string{
		`ExecStart="/opt/my tools/mds" --foreground --service run`,
		`Environment="` + editorEnv + `=code -g"`,
		"StandardOutput=null",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unitFile, want) {
			t.Errorf("systemd unit is missing %q:\n%s", want, unitFile)
		}
	}
	if strings.Contains(unitFile, "Restart=always") {
		t.Errorf("systemd unit restarts always, so systemd would undo mds --stop:\n%s", unitFile)
	}
}

func TestServiceStateSeesWhichMdsIsInstalled(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	u := unit{exe: "/opt/mds/mds", args: []string{"--foreground", "--service", "run"}}
	if installed, _ := serviceState(u); installed {
		t.Fatal("serviceState found a login item in an empty config dir")
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
		t.Errorf("serviceState = %v, %v for the unit it just wrote, want true, true", installed, ours)
	}
	moved := unit{exe: "/usr/local/bin/mds", args: u.args}
	if installed, ours := serviceState(moved); !installed || ours {
		t.Errorf("serviceState = %v, %v for another binary, want true, false, "+
			"so status can say the login item points elsewhere", installed, ours)
	}
}

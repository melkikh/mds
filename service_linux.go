package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// systemd owns the process, so the login item runs the server in this terminal that isn't
// one rather than detaching from under it.
const serviceForeground = true

const serviceUnitName = serviceLabel + ".service"

func serviceLocation() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "systemd", "user", serviceUnitName), nil
}

// serviceContent is the unit systemd reads. Restart=on-failure and nothing more: mds --stop
// leaves through the front door with status 0, and has to stay stopped when it does.
func serviceContent(u unit) string {
	var out strings.Builder
	out.WriteString("[Unit]\nDescription=mds — markdown in the browser\n\n")
	out.WriteString("[Service]\n")
	fmt.Fprintf(&out, "ExecStart=%s\n", serviceCommand(u))
	for _, setting := range u.env {
		fmt.Fprintf(&out, "Environment=%s\n", quoteUnit(setting))
	}
	out.WriteString("Restart=on-failure\n")
	// the url mds prints carries the key that unlocks it, and the journal is not the place
	// for it
	out.WriteString("StandardOutput=null\n")
	out.WriteString("\n[Install]\nWantedBy=default.target\n")
	return out.String()
}

func serviceInstalled() bool {
	path, err := serviceLocation()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func serviceInstall(u unit) error {
	path, err := serviceLocation()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(serviceContent(u)), 0o644); err != nil {
		return err
	}
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	if err := systemctl("enable", serviceUnitName); err != nil {
		return err
	}
	return systemctl("restart", serviceUnitName)
}

func serviceRemove() error {
	path, err := serviceLocation()
	if err != nil {
		return err
	}
	_ = systemctl("disable", "--now", serviceUnitName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return systemctl("daemon-reload")
}

// serviceCommand writes the binary as one word whatever its path looks like, and leaves the
// flags alone so the unit stays readable.
func serviceCommand(u unit) string {
	spoken := []string{quoteUnit(u.exe)}
	for _, arg := range u.args {
		if strings.ContainsAny(arg, " \t\"'\\") {
			arg = quoteUnit(arg)
		}
		spoken = append(spoken, arg)
	}
	return strings.Join(spoken, " ")
}

var unitEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

func quoteUnit(value string) string {
	return `"` + unitEscaper.Replace(value) + `"`
}

func systemctl(args ...string) error {
	path, err := exec.LookPath("systemctl")
	if err != nil {
		return fmt.Errorf("%w: there is no systemctl to put it in", errNoService)
	}
	out, err := exec.Command(path, append([]string{"--user"}, args...)...).CombinedOutput()
	if err == nil {
		return nil
	}
	if said := strings.TrimSpace(string(out)); said != "" {
		return fmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), said)
	}
	return fmt.Errorf("systemctl --user %s: %w", strings.Join(args, " "), err)
}

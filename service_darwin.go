package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// launchd owns the process, so the login item runs the server in this terminal that isn't
// one rather than detaching from under it.
const serviceForeground = true

func serviceLocation() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist"), nil
}

// serviceContent is the job launchd reads. There is deliberately no KeepAlive: mds --stop
// has to mean stopped until the next login, not stopped for the second it takes launchd to
// start it again.
func serviceContent(u unit) string {
	var out strings.Builder
	out.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	out.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" ` +
		`"http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	out.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	fmt.Fprintf(&out, "  <key>Label</key>\n  <string>%s</string>\n", serviceLabel)
	out.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, arg := range u.argv() {
		fmt.Fprintf(&out, "    <string>%s</string>\n", xmlText(arg))
	}
	out.WriteString("  </array>\n")
	out.WriteString("  <key>RunAtLoad</key>\n  <true/>\n")
	if len(u.env) > 0 {
		out.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
		for _, setting := range u.env {
			name, value, _ := strings.Cut(setting, "=")
			fmt.Fprintf(&out, "    <key>%s</key>\n    <string>%s</string>\n", xmlText(name), xmlText(value))
		}
		out.WriteString("  </dict>\n")
	}
	out.WriteString("</dict>\n</plist>\n")
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
	// a job that is already loaded refuses to load again, and this is also how a reinstall
	// gets launchd to read the file we just wrote
	_ = launchctl("bootout", serviceTarget())
	_ = launchctl("enable", serviceTarget())
	if err := launchctl("bootstrap", serviceDomain(), path); err != nil {
		return err
	}
	return launchctl("kickstart", "-k", serviceTarget())
}

func serviceRemove() error {
	path, err := serviceLocation()
	if err != nil {
		return err
	}
	_ = launchctl("bootout", serviceTarget())
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func serviceDomain() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

func serviceTarget() string {
	return serviceDomain() + "/" + serviceLabel
}

func launchctl(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err == nil {
		return nil
	}
	if said := strings.TrimSpace(string(out)); said != "" {
		return fmt.Errorf("launchctl %s: %s", strings.Join(args, " "), said)
	}
	return fmt.Errorf("launchctl %s: %w", strings.Join(args, " "), err)
}

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func xmlText(value string) string {
	return xmlEscaper.Replace(value)
}

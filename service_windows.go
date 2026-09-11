package main

import (
	"errors"
	"os/exec"

	"golang.org/x/sys/windows/registry"
)

// Nothing supervises a login item under Run, so mds detaches from it the way it detaches
// from a terminal: the process that logon starts prints and leaves, the server stays.
const serviceForeground = false

// A Run entry inherits the user's environment and carries none of its own, so MDS_EDITOR
// and its neighbours have to be set as user environment variables to reach the login server.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func serviceLocation() (string, error) {
	return `HKCU\` + runKey + `\` + serviceLabel, nil
}

func serviceInstalled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer func() { _ = key.Close() }()
	_, _, err = key.GetStringValue(serviceLabel)
	return err == nil
}

func serviceInstall(u unit) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = key.Close() }()
	if err := key.SetStringValue(serviceLabel, u.command()); err != nil {
		return err
	}
	return serviceRestart(u)
}

func serviceRemove() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = key.Close() }()
	if err := key.DeleteValue(serviceLabel); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// There is no job to kick here, so restarting is stopping whatever mds holds the shared
// port and starting the login server in its place.
func serviceRestart(u unit) error {
	for _, running := range readInstances() {
		if running.port != sharedPort() {
			continue
		}
		_, _ = call(running, "/_stop")
		dropInstance(running.port)
	}
	child := exec.Command(u.exe, u.args...)
	child.SysProcAttr = detachAttr()
	return child.Start()
}

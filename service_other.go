//go:build !darwin && !linux && !windows

package main

// Nobody here has a per-user service manager mds could agree with, so --service status
// prints the command to put in whatever starts your session, and the rest says so.
const serviceForeground = true

func serviceLocation() (string, error) { return "", errNoService }

func serviceState(unit) (installed, ours bool) { return false, false }

func serviceInstall(unit) error { return errNoService }

func serviceRemove() error { return errNoService }

func serviceRestart(unit) error { return errNoService }

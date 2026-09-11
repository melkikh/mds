//go:build !darwin && !linux && !windows

package main

// Nobody here has a per-user service manager mds could agree with, so installation says so.
const serviceForeground = true

func serviceLocation() (string, error) { return "", errNoService }

func serviceInstalled() bool { return false }

func serviceInstall(unit) error { return errNoService }

func serviceRemove() error { return errNoService }

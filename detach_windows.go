package main

import "syscall"

const createNewProcessGroup = 0x00000200

func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

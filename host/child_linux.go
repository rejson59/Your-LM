package main

import (
	"os/exec"
	"syscall"
)

func prepareChild(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL} }
func trackChild(c *exec.Cmd)   {}

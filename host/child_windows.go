package main

import "os/exec"

// The controller stops children during its normal shutdown. Avoid process
// injection/manipulation APIs; runtime programs are verified before launch.
func prepareChild(*exec.Cmd) {}
func trackChild(*exec.Cmd)   {}

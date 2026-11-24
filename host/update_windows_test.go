//go:build windows

package main

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestUpdateHelperArgumentsIncludeParentPID(t *testing.T) {
	target, pid, active := helperArguments([]string{
		"YourLM.exe", "--apply-update", `D:\YourLM\App\YourLM.exe`, "--wait-pid", "4242",
	})
	if !active || target != `D:\YourLM\App\YourLM.exe` || pid != 4242 {
		t.Fatalf("unexpected helper arguments: active=%v target=%q pid=%d", active, target, pid)
	}
}

func TestWaitForUpdateParent(t *testing.T) {
	if os.Getenv("YOURLM_UPDATE_WAIT_HELPER") == "1" {
		time.Sleep(250 * time.Millisecond)
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestWaitForUpdateParent$")
	command.Env = append(os.Environ(), "YOURLM_UPDATE_WAIT_HELPER=1")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	started := time.Now()
	if err := waitForUpdateParent(command.Process.Pid, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 100*time.Millisecond {
		t.Fatalf("update wait returned before the parent exited: %s", elapsed)
	}
}

//go:build !windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

var (
	sigStop = syscall.SIGSTOP
	sigCont = syscall.SIGCONT
	sigTerm = syscall.SIGTERM
)

func startBackground(id int64) (int, error) {
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(self, "run", strconv.FormatInt(id, 10))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	cmd.Process.Release()
	return pid, nil
}

func signalJob(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("el job no tiene proceso en background activo")
	}
	return syscall.Kill(-pid, sig)
}

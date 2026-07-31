//go:build windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

var (
	sigStop = syscall.Signal(0)
	sigCont = syscall.Signal(0)
	sigTerm = syscall.Signal(0)
)

// startBackground lanza `git-gost run <id>` como proceso independiente.
func startBackground(id int64) (int, error) {
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(self, "run", strconv.FormatInt(id, 10))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
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
	// Capturar el PID antes de Release: en Go 1.25, Release invalida Pid (-1).
	pid := cmd.Process.Pid
	cmd.Process.Release()
	return pid, nil
}

// signalJob no está soportado en Windows en esta fase: no hay pausa real por
// señales y cancelar un proceso en ejecución requiere taskkill.
func signalJob(pid int, sig syscall.Signal) error {
	return fmt.Errorf("pause/resume/cancel en ejecución no está soportado en Windows en esta fase")
}

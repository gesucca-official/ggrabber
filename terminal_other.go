//go:build !linux

package main

import (
	"os/exec"
	"runtime"
)

// Su Windows e macOS il doppio clic apre già un terminale.
func relaunchInTerminal() bool { return false }

func clipboardCmd() *exec.Cmd {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("clip")
	case "darwin":
		return exec.Command("pbcopy")
	}
	return nil
}

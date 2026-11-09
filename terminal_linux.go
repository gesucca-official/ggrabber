package main

import (
	"os"
	"os/exec"
)

// relaunchInTerminal: su Linux, aprendo l'eseguibile con doppio clic dal file manager non
// si apre nessun terminale. In quel caso ci si rilancia dentro il primo emulatore trovato.
// Restituisce true se il rilancio è avvenuto (e quindi questo processo deve uscire).
func relaunchInTerminal() bool {
	if isTerminal() || os.Getenv("GGRABBER_IN_TERMINAL") != "" {
		return false
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return false // niente desktop: probabilmente lanciato da cron o da script
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	self := append([]string{exe}, os.Args[1:]...)
	terminals := []struct {
		name string
		flag []string // argomenti prima del comando da eseguire
	}{
		{"x-terminal-emulator", []string{"-e"}},
		{"gnome-terminal", []string{"--"}},
		{"konsole", []string{"-e"}},
		{"xfce4-terminal", []string{"-x"}},
		{"mate-terminal", []string{"-x"}},
		{"tilix", []string{"-e"}},
		{"alacritty", []string{"-e"}},
		{"kitty", nil},
		{"foot", nil},
		{"xterm", []string{"-e"}},
	}
	for _, t := range terminals {
		path, err := exec.LookPath(t.name)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, append(append([]string{}, t.flag...), self...)...)
		cmd.Env = append(os.Environ(), "GGRABBER_IN_TERMINAL=1")
		if cmd.Start() == nil {
			return true
		}
	}
	return false
}

func clipboardCmd() *exec.Cmd {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if _, err := exec.LookPath("wl-copy"); err == nil {
			return exec.Command("wl-copy")
		}
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		return exec.Command("xclip", "-selection", "clipboard")
	}
	if _, err := exec.LookPath("xsel"); err == nil {
		return exec.Command("xsel", "--clipboard", "--input")
	}
	return nil
}

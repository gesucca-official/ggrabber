package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// ---------- log su console + file ----------

var (
	logMu   sync.Mutex
	logFile io.Writer = io.Discard
)

func openLog(path string) func() {
	f, err := os.Create(path)
	if err != nil {
		return func() {}
	}
	logMu.Lock()
	logFile = f
	logMu.Unlock()
	return func() {
		logMu.Lock()
		logFile = io.Discard
		logMu.Unlock()
		f.Close()
	}
}

func logf(format string, a ...any) {
	s := fmt.Sprintf(format, a...)
	logMu.Lock()
	defer logMu.Unlock()
	fmt.Println(s)
	fmt.Fprintln(logFile, s)
}

// ---------- input da terminale ----------

var stdin = bufio.NewReader(os.Stdin)

func isTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

func readLine() string {
	s, _ := stdin.ReadString('\n')
	return strings.TrimSpace(s)
}

func readSecret() string {
	if isTerminal() {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return readLine()
}

func askYesNo(question string, def bool) bool {
	suffix := " [S/n] "
	if !def {
		suffix = " [s/N] "
	}
	fmt.Print(question + suffix)
	a := strings.ToLower(readLine())
	if a == "" {
		return def
	}
	return a == "s" || a == "si" || a == "sì" || a == "y" || a == "yes"
}

// ---------- browser e appunti ----------

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func copyToClipboard(text string) bool {
	cmd := clipboardCmd()
	if cmd == nil {
		return false
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run() == nil
}

// ---------- file ----------

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, b)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// safeName rende un nome utilizzabile come nome di file su Windows e macOS.
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	out := strings.TrimRight(b.String(), ". ")
	if out == "" {
		out = "_"
	}
	return out
}

func dirSize(root string) int64 {
	var n int64
	filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

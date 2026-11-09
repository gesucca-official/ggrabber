package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git esegue il git di sistema passando il token tramite GIT_ASKPASS (questo stesso
// eseguibile), così il token non finisce né negli argomenti né in .git/config.
type Git struct {
	env    []string
	hasLFS bool
}

func newGit(token string) (*Git, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	env := append(os.Environ(),
		"GIT_ASKPASS="+exe,
		"GGRABBER_ASKPASS=1",
		"GGRABBER_TOKEN="+token,
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
	)
	_, lfsErr := exec.LookPath("git-lfs")
	return &Git{env: env, hasLFS: lfsErr == nil}, nil
}

func (g *Git) run(ctx context.Context, args ...string) (string, error) {
	// credential.helper vuoto: ignora eventuali credenziali salvate di altri account.
	// safe.directory=*: sui dischi di rete (NAS) il proprietario dei file non coincide con
	// l'utente locale e git altrimenti rifiuterebbe di operare sui mirror già esistenti.
	full := append([]string{"-c", "credential.helper=", "-c", "core.longpaths=true", "-c", "safe.directory=*"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = g.env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return out.String(), fmt.Errorf("git %s: %s", args[0], lastLines(out.String(), 3))
	}
	return out.String(), nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, " | ")
}

var errNotFound = errors.New("repo non trovata")

// syncMirror crea (clone --mirror) o aggiorna (remote update --prune) un mirror bare.
// Un clone interrotto non lascia mai una cartella a metà: si clona in .partial e poi si rinomina.
func (g *Git) syncMirror(ctx context.Context, url, dir string) (created bool, err error) {
	if exists(dir) {
		out, err := g.run(ctx, "-C", dir, "rev-parse", "--is-bare-repository")
		if err != nil || strings.TrimSpace(out) != "true" {
			return false, fmt.Errorf("%s esiste ma non è un mirror git valido", dir)
		}
		if _, err := g.run(ctx, "-C", dir, "remote", "set-url", "origin", url); err != nil {
			return false, err
		}
		if _, err := g.run(ctx, "-C", dir, "remote", "update", "--prune"); err != nil {
			return false, err
		}
		g.run(ctx, "-C", dir, "gc", "--auto", "--quiet")
		return false, nil
	}

	tmp := dir + ".partial"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return false, err
	}
	out, err := g.run(ctx, "clone", "--mirror", "--quiet", url, tmp)
	if err != nil {
		os.RemoveAll(tmp)
		low := strings.ToLower(out)
		if strings.Contains(low, "not found") || strings.Contains(low, "does not appear to be a git repository") {
			return false, errNotFound
		}
		return false, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return false, err
	}
	return true, nil
}

// usesLFS controlla se il branch di default usa Git LFS.
func (g *Git) usesLFS(ctx context.Context, dir string) bool {
	out, err := g.run(ctx, "-C", dir, "show", "HEAD:.gitattributes")
	return err == nil && strings.Contains(out, "filter=lfs")
}

func (g *Git) fetchLFS(ctx context.Context, dir string) error {
	_, err := g.run(ctx, "-C", dir, "lfs", "fetch", "--all", "origin")
	return err
}

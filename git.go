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
	"time"
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

// historyPrefix: qui finiscono i ref che GitHub ha riscritto (force-push, tag spostati),
// così nessun commit già presente nel backup può andare perso.
const historyPrefix = "refs/ggrabber-history/"

// syncMirror crea (clone --mirror) o aggiorna un mirror bare.
// L'aggiornamento non cancella mai nulla: i branch eliminati su GitHub restano, e i ref
// riscritti in modo non fast-forward vengono prima salvati sotto refs/ggrabber-history/.
// Un clone interrotto non lascia mai una cartella a metà: si clona in .partial e poi si rinomina.
func (g *Git) syncMirror(ctx context.Context, url, dir string) (created bool, saved int, err error) {
	if exists(dir) {
		out, err := g.run(ctx, "-C", dir, "rev-parse", "--is-bare-repository")
		if err != nil || strings.TrimSpace(out) != "true" {
			return false, 0, fmt.Errorf("%s esiste ma non è un mirror git valido", dir)
		}
		if _, err := g.run(ctx, "-C", dir, "remote", "set-url", "origin", url); err != nil {
			return false, 0, err
		}
		before, err := g.refs(ctx, dir)
		if err != nil {
			return false, 0, err
		}
		// --no-prune esplicito: vince anche su un eventuale fetch.prune=true nella config globale.
		if _, err := g.run(ctx, "-C", dir, "fetch", "--no-prune", "--quiet", "origin"); err != nil {
			return false, 0, err
		}
		saved, err = g.saveRewritten(ctx, dir, before)
		if err != nil {
			return false, saved, fmt.Errorf("salvataggio storico ref: %w", err)
		}
		g.run(ctx, "-C", dir, "gc", "--auto", "--quiet")
		return false, saved, nil
	}

	tmp := dir + ".partial"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return false, 0, err
	}
	out, err := g.run(ctx, "clone", "--mirror", "--quiet", url, tmp)
	if err != nil {
		os.RemoveAll(tmp)
		low := strings.ToLower(out)
		if strings.Contains(low, "not found") || strings.Contains(low, "does not appear to be a git repository") {
			return false, 0, errNotFound
		}
		return false, 0, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return false, 0, err
	}
	return true, 0, nil
}

// refs restituisce ref -> sha, escluso lo storico.
func (g *Git) refs(ctx context.Context, dir string) (map[string]string, error) {
	out, err := g.run(ctx, "-C", dir, "for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && !strings.HasPrefix(ref, historyPrefix) {
			m[ref] = sha
		}
	}
	return m, nil
}

// saveRewritten confronta i ref prima e dopo il fetch e conserva il vecchio valore di
// quelli spariti o aggiornati in modo non fast-forward.
func (g *Git) saveRewritten(ctx context.Context, dir string, before map[string]string) (int, error) {
	after, err := g.refs(ctx, dir)
	if err != nil {
		return 0, err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	var cmds strings.Builder
	n := 0
	for ref, old := range before {
		// refs/pull/N/merge è un merge di prova che GitHub ricalcola di continuo: non è storia.
		if strings.HasPrefix(ref, "refs/pull/") && strings.HasSuffix(ref, "/merge") {
			continue
		}
		cur, ok := after[ref]
		if ok && cur == old {
			continue
		}
		if ok {
			// fast-forward: il vecchio commit è ancora raggiungibile, niente da salvare
			if _, err := g.run(ctx, "-C", dir, "merge-base", "--is-ancestor", old, cur); err == nil {
				continue
			}
		}
		fmt.Fprintf(&cmds, "create %s%s/%s %s\n", historyPrefix, stamp, strings.TrimPrefix(ref, "refs/"), old)
		n++
	}
	if n == 0 {
		return 0, nil
	}
	cmd := exec.CommandContext(ctx, "git", "-c", "safe.directory=*", "-C", dir, "update-ref", "--stdin")
	cmd.Env = g.env
	cmd.Stdin = strings.NewReader(cmds.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("%s", lastLines(string(out), 3))
	}
	return n, nil
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

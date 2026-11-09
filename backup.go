package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const stateDirName = "_ggrabber"

type result struct {
	name     string
	created  bool
	err      error
	notes    []string
	warnings []string
}

func (r result) line() string {
	var s string
	switch {
	case r.err != nil:
		s = r.name + "  ERRORE: " + r.err.Error()
	case r.created:
		s = r.name + "  nuova"
	default:
		s = r.name + "  aggiornata"
	}
	if len(r.notes) > 0 {
		s += " (" + strings.Join(r.notes, ", ") + ")"
	}
	return s
}

// ---------- stato (per riconoscere le repo rinominate/trasferite) ----------

type state struct {
	Account string            `json:"account"`
	LastRun string            `json:"last_run"`
	Repos   map[string]string `json:"repos"` // id -> owner/name
}

func loadState(root string) *state {
	st := &state{Repos: map[string]string{}}
	b, err := os.ReadFile(filepath.Join(root, stateDirName, "state.json"))
	if err == nil {
		json.Unmarshal(b, st)
	}
	if st.Repos == nil {
		st.Repos = map[string]string{}
	}
	return st
}

func (st *state) save(root string) error {
	return writeJSON(filepath.Join(root, stateDirName, "state.json"), st)
}

func repoBase(root, fullName string) string {
	return filepath.Join(root, filepath.FromSlash(fullName))
}

// handleRenames sposta le cartelle di repo rinominate o trasferite, invece di riscaricarle.
func handleRenames(root string, st *state, repos []*Repo) {
	for _, r := range repos {
		id := fmt.Sprint(r.ID)
		old, ok := st.Repos[id]
		if ok && !strings.EqualFold(old, r.FullName) {
			oldBase, newBase := repoBase(root, old), repoBase(root, r.FullName)
			moved := false
			for _, suf := range []string{".git", ".wiki.git", ".meta"} {
				if exists(oldBase+suf) && !exists(newBase+suf) {
					os.MkdirAll(filepath.Dir(newBase), 0o755)
					if os.Rename(oldBase+suf, newBase+suf) == nil {
						moved = true
					}
				}
			}
			if moved {
				logf("Rinominata: %s -> %s (cartelle spostate)", old, r.FullName)
			}
		}
		st.Repos[id] = r.FullName
	}
}

// ---------- elaborazione ----------

func processRepo(ctx context.Context, c *Client, g *Git, root string, r *Repo, noMeta bool) result {
	res := result{name: r.FullName}
	base := repoBase(root, r.FullName)
	mirror := base + ".git"

	if !exists(mirror) && r.Size > 50*1024 {
		logf("   ...scarico %s (circa %s), può volerci un po'", r.FullName, humanBytes(r.Size*1024))
	}
	created, saved, err := g.syncMirror(ctx, r.CloneURL, mirror)
	if err != nil {
		res.err = err
		return res
	}
	res.created = created
	if saved > 0 {
		res.warnings = append(res.warnings, fmt.Sprintf("%d ref riscritti su GitHub (force-push?): versioni precedenti conservate in %s", saved, historyPrefix))
	}

	if g.usesLFS(ctx, mirror) {
		if g.hasLFS {
			if err := g.fetchLFS(ctx, mirror); err != nil {
				res.warnings = append(res.warnings, "file LFS non scaricati: "+err.Error())
			}
		} else {
			res.warnings = append(res.warnings, "usa Git LFS ma git-lfs non è installato: i file LFS non sono nel backup")
		}
	}

	if r.HasWiki {
		wikiURL := strings.TrimSuffix(r.CloneURL, ".git") + ".wiki.git"
		if _, _, err := g.syncMirror(ctx, wikiURL, base+".wiki.git"); err == nil {
			res.notes = append(res.notes, "wiki")
		} else if !errors.Is(err, errNotFound) {
			res.warnings = append(res.warnings, "wiki: "+err.Error())
		}
	}

	if !noMeta {
		notes, warns := exportMeta(ctx, c, r, base+".meta")
		res.notes = append(res.notes, notes...)
		res.warnings = append(res.warnings, warns...)
	}
	if ctx.Err() != nil && res.err == nil {
		res.err = ctx.Err()
	}
	return res
}

func processGist(ctx context.Context, g *Git, root string, gi *Gist) result {
	res := result{name: "gist " + gi.ID}
	dir := filepath.Join(root, "_gists")
	created, _, err := g.syncMirror(ctx, gi.GitPullURL, filepath.Join(dir, gi.ID+".git"))
	if err != nil {
		res.err = err
		return res
	}
	res.created = created
	if err := writeFileAtomic(filepath.Join(dir, gi.ID+".json"), indent(gi.Raw)); err != nil {
		res.warnings = append(res.warnings, err.Error())
	}
	return res
}

func indent(raw json.RawMessage) []byte {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return b
}

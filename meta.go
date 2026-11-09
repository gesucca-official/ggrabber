package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Layout di <owner>/<repo>.meta/:
//   repo.json, labels.json, milestones.json
//   issues/<numero>.json   issue o PR con commenti (+ dettagli PR, review, commenti di review)
//   releases/releases.json, releases/<tag>/<asset>
//   sync.json              punto da cui riprendere al prossimo giro

type metaSync struct {
	IssuesSince string `json:"issues_since"`
}

func exportMeta(ctx context.Context, c *Client, r *Repo, dir string) (notes, warns []string) {
	warn := func(what string, err error) {
		if ctx.Err() == nil {
			warns = append(warns, what+": "+err.Error())
		}
	}

	if err := writeFileAtomic(filepath.Join(dir, "repo.json"), indent(r.Raw)); err != nil {
		warn("repo.json", err)
		return
	}
	for _, name := range []string{"labels", "milestones"} {
		u := fmt.Sprintf("/repos/%s/%s?per_page=100", r.FullName, name)
		if name == "milestones" {
			u += "&state=all"
		}
		items, err := c.collect(ctx, u)
		if err != nil {
			if !isStatus(err, 404, 410) {
				warn(name, err)
			}
			continue
		}
		if err := writeJSON(filepath.Join(dir, name+".json"), items); err != nil {
			warn(name, err)
		}
	}

	syncPath := filepath.Join(dir, "sync.json")
	var ms metaSync
	if b, err := os.ReadFile(syncPath); err == nil {
		json.Unmarshal(b, &ms)
	}
	n, err := exportIssues(ctx, c, r, dir, &ms)
	writeJSON(syncPath, ms) // salva comunque il progresso parziale
	if err != nil {
		warn("issue/PR", err)
	}
	if n > 0 {
		notes = append(notes, fmt.Sprintf("%d issue/PR", n))
	}

	a, err := exportReleases(ctx, c, r, filepath.Join(dir, "releases"))
	if err != nil {
		warn("release", err)
	}
	if a > 0 {
		notes = append(notes, fmt.Sprintf("%d asset", a))
	}
	return
}

type issueHead struct {
	Number      int             `json:"number"`
	Comments    int             `json:"comments"`
	UpdatedAt   string          `json:"updated_at"`
	PullRequest json.RawMessage `json:"pull_request"`
}

// exportIssues scarica solo le issue/PR modificate dall'ultimo giro.
func exportIssues(ctx context.Context, c *Client, r *Repo, dir string, ms *metaSync) (int, error) {
	count := 0
	u := fmt.Sprintf("/repos/%s/issues?state=all&sort=updated&direction=asc&per_page=100", r.FullName)
	if ms.IssuesSince != "" {
		u += "&since=" + ms.IssuesSince
	}
	// Ordinate per data di modifica crescente: si può salvare il progresso man mano.
	err := c.paginate(ctx, u, func(raw json.RawMessage) (bool, error) {
		var h issueHead
		if err := json.Unmarshal(raw, &h); err != nil {
			return false, err
		}
		if err := saveIssue(ctx, c, r, dir, raw, h, h.PullRequest != nil); err != nil {
			return false, err
		}
		if h.UpdatedAt > ms.IssuesSince {
			ms.IssuesSince = h.UpdatedAt
		}
		count++
		return true, nil
	})
	if isStatus(err, 410) {
		// Issue disabilitate nella repo: le PR vanno prese dall'endpoint /pulls.
		return exportPullsOnly(ctx, c, r, dir, ms)
	}
	if isStatus(err, 404) {
		return count, nil
	}
	return count, err
}

func exportPullsOnly(ctx context.Context, c *Client, r *Repo, dir string, ms *metaSync) (int, error) {
	count := 0
	newest := ms.IssuesSince
	u := fmt.Sprintf("/repos/%s/pulls?state=all&sort=updated&direction=desc&per_page=100", r.FullName)
	err := c.paginate(ctx, u, func(raw json.RawMessage) (bool, error) {
		var h issueHead
		if err := json.Unmarshal(raw, &h); err != nil {
			return false, err
		}
		if ms.IssuesSince != "" && h.UpdatedAt < ms.IssuesSince {
			return false, nil
		}
		if h.UpdatedAt > newest {
			newest = h.UpdatedAt
		}
		h.Comments = 1 // sconosciuto dalla lista: prova sempre a leggere i commenti
		if err := saveIssue(ctx, c, r, dir, nil, h, true); err != nil {
			return false, err
		}
		count++
		return true, nil
	})
	if err == nil {
		ms.IssuesSince = newest // ordine decrescente: avanza solo se completato
	}
	if isStatus(err, 404) {
		return count, nil
	}
	return count, err
}

func saveIssue(ctx context.Context, c *Client, r *Repo, dir string, issue json.RawMessage, h issueHead, isPull bool) error {
	doc := map[string]any{}
	if issue != nil {
		doc["issue"] = issue
	}
	base := fmt.Sprintf("/repos/%s", r.FullName)
	if h.Comments > 0 {
		cm, err := c.collect(ctx, fmt.Sprintf("%s/issues/%d/comments?per_page=100", base, h.Number))
		if err != nil && !isStatus(err, 404, 410) {
			return err
		}
		doc["comments"] = cm
	}
	if isPull {
		var pull json.RawMessage
		if _, err := c.getJSON(ctx, fmt.Sprintf("%s/pulls/%d", base, h.Number), &pull); err != nil {
			if !isStatus(err, 404) {
				return err
			}
		} else {
			doc["pull"] = pull
		}
		for key, path := range map[string]string{"review_comments": "comments", "reviews": "reviews"} {
			items, err := c.collect(ctx, fmt.Sprintf("%s/pulls/%d/%s?per_page=100", base, h.Number, path))
			if err != nil && !isStatus(err, 404) {
				return err
			}
			doc[key] = items
		}
	}
	return writeJSON(filepath.Join(dir, "issues", fmt.Sprintf("%d.json", h.Number)), doc)
}

// exportReleases salva l'elenco release e scarica gli asset non ancora presenti.
func exportReleases(ctx context.Context, c *Client, r *Repo, dir string) (int, error) {
	rels, err := c.collect(ctx, fmt.Sprintf("/repos/%s/releases?per_page=100", r.FullName))
	if err != nil {
		if isStatus(err, 404) {
			return 0, nil
		}
		return 0, err
	}
	if len(rels) == 0 {
		return 0, nil
	}
	if err := writeJSON(filepath.Join(dir, "releases.json"), rels); err != nil {
		return 0, err
	}
	downloaded := 0
	for _, raw := range rels {
		var rel struct {
			TagName string `json:"tag_name"`
			ID      int64  `json:"id"`
			Assets  []struct {
				Name string `json:"name"`
				Size int64  `json:"size"`
				URL  string `json:"url"`
			} `json:"assets"`
		}
		if err := json.Unmarshal(raw, &rel); err != nil {
			return downloaded, err
		}
		tag := rel.TagName
		if tag == "" {
			tag = fmt.Sprintf("draft-%d", rel.ID)
		}
		for _, a := range rel.Assets {
			dst := filepath.Join(dir, safeName(tag), safeName(a.Name))
			if fi, err := os.Stat(dst); err == nil && fi.Size() == a.Size {
				continue
			}
			if err := c.download(ctx, a.URL, dst); err != nil {
				return downloaded, fmt.Errorf("asset %s/%s: %w", tag, a.Name, err)
			}
			downloaded++
		}
	}
	return downloaded, nil
}

func (c *Client) download(ctx context.Context, url, dst string) error {
	resp, err := c.get(ctx, url, "application/octet-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const apiBase = "https://api.github.com"

type Client struct {
	token string
	hc    *http.Client
}

func newClient(token string) *Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
		MaxIdleConnsPerHost:   8,
	}
	return &Client{token: token, hc: &http.Client{Transport: tr}}
}

type User struct {
	Login string `json:"login"`
	Name  string `json:"name"`
}

type Repo struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	FullName  string `json:"full_name"`
	Private   bool   `json:"private"`
	Fork      bool   `json:"fork"`
	Archived  bool   `json:"archived"`
	HasWiki   bool   `json:"has_wiki"`
	HasIssues bool   `json:"has_issues"`
	CloneURL  string `json:"clone_url"`
	Size      int64  `json:"size"` // KB
	Owner     struct {
		Login string `json:"login"`
	} `json:"owner"`
	Raw json.RawMessage `json:"-"`
}

type Gist struct {
	ID         string          `json:"id"`
	GitPullURL string          `json:"git_pull_url"`
	Raw        json.RawMessage `json:"-"`
}

type apiError struct {
	Status int
	URL    string
	Msg    string
	SSO    bool
}

func (e *apiError) Error() string {
	if e.SSO {
		return "accesso bloccato dal SAML SSO dell'organizzazione: autorizza il token per quell'organizzazione su GitHub"
	}
	return fmt.Sprintf("HTTP %d su %s: %s", e.Status, e.URL, e.Msg)
}

func isStatus(err error, codes ...int) bool {
	var ae *apiError
	if errors.As(err, &ae) {
		for _, c := range codes {
			if ae.Status == c {
				return true
			}
		}
	}
	return false
}

// get esegue una GET con gestione dei rate limit e dei retry.
func (c *Client) get(ctx context.Context, url, accept string) (*http.Response, error) {
	if !strings.HasPrefix(url, "http") {
		url = apiBase + url
	}
	failures := 0
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", accept)
		req.Header.Set("User-Agent", "ggrabber/"+version)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := c.hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			failures++
			if failures > 4 {
				return nil, err
			}
			if err := sleepCtx(ctx, time.Duration(failures*failures)*2*time.Second); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode < 300 {
			return resp, nil
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()

		if wait, ok := rateLimitWait(resp, body); ok {
			logf("   Limite API di GitHub raggiunto: attendo %s e riprendo...", wait.Round(time.Second))
			if err := sleepCtx(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode >= 500 && failures < 4 {
			failures++
			if err := sleepCtx(ctx, time.Duration(failures*failures)*2*time.Second); err != nil {
				return nil, err
			}
			continue
		}
		var m struct {
			Message string `json:"message"`
		}
		json.Unmarshal(body, &m)
		return nil, &apiError{
			Status: resp.StatusCode,
			URL:    strings.TrimPrefix(url, apiBase),
			Msg:    m.Message,
			SSO:    resp.Header.Get("X-GitHub-SSO") != "",
		}
	}
}

func rateLimitWait(resp *http.Response, body []byte) (time.Duration, bool) {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return 0, false
	}
	if s := resp.Header.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return time.Duration(n+1) * time.Second, true
		}
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if n, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			d := time.Until(time.Unix(n, 0)) + 2*time.Second
			if d < time.Second {
				d = time.Second
			}
			return d, true
		}
	}
	if strings.Contains(strings.ToLower(string(body)), "secondary rate limit") {
		return time.Minute, true
	}
	return 0, false
}

func (c *Client) getJSON(ctx context.Context, url string, v any) (http.Header, error) {
	resp, err := c.get(ctx, url, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return resp.Header, json.NewDecoder(resp.Body).Decode(v)
}

// paginate scorre tutte le pagine; fn restituisce false per fermarsi.
func (c *Client) paginate(ctx context.Context, url string, fn func(json.RawMessage) (bool, error)) error {
	for url != "" {
		resp, err := c.get(ctx, url, "application/vnd.github+json")
		if err != nil {
			return err
		}
		var items []json.RawMessage
		err = json.NewDecoder(resp.Body).Decode(&items)
		resp.Body.Close()
		if err != nil {
			return err
		}
		for _, it := range items {
			more, err := fn(it)
			if err != nil {
				return err
			}
			if !more {
				return nil
			}
		}
		url = nextLink(resp.Header.Get("Link"))
	}
	return nil
}

func (c *Client) collect(ctx context.Context, url string) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	err := c.paginate(ctx, url, func(m json.RawMessage) (bool, error) {
		out = append(out, m)
		return true, nil
	})
	return out, err
}

func nextLink(h string) string {
	for _, part := range strings.Split(h, ",") {
		seg := strings.Split(part, ";")
		if len(seg) < 2 {
			continue
		}
		for _, p := range seg[1:] {
			if strings.TrimSpace(p) == `rel="next"` {
				return strings.Trim(strings.TrimSpace(seg[0]), "<>")
			}
		}
	}
	return ""
}

func (c *Client) whoami(ctx context.Context) (*User, string, error) {
	var u User
	h, err := c.getJSON(ctx, "/user", &u)
	if err != nil {
		return nil, "", err
	}
	return &u, h.Get("X-OAuth-Scopes"), nil
}

func (c *Client) listRepos(ctx context.Context) ([]*Repo, error) {
	var repos []*Repo
	seen := map[int64]bool{}
	err := c.paginate(ctx, "/user/repos?per_page=100&visibility=all&affiliation=owner,collaborator,organization_member",
		func(m json.RawMessage) (bool, error) {
			r := &Repo{Raw: m}
			if err := json.Unmarshal(m, r); err != nil {
				return false, err
			}
			if !seen[r.ID] {
				seen[r.ID] = true
				repos = append(repos, r)
			}
			return true, nil
		})
	return repos, err
}

func (c *Client) listGists(ctx context.Context) ([]*Gist, error) {
	var gists []*Gist
	err := c.paginate(ctx, "/gists?per_page=100", func(m json.RawMessage) (bool, error) {
		g := &Gist{Raw: m}
		if err := json.Unmarshal(m, g); err != nil {
			return false, err
		}
		gists = append(gists, g)
		return true, nil
	})
	return gists, err
}

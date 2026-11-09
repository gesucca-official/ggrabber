package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
	return strings.TrimSpace(string(out))
}

// Il mirror non deve mai perdere commit: branch cancellati restano, ref riscritti finiscono nello storico.
func TestMirrorKeepsHistory(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	work, origin := filepath.Join(tmp, "work"), filepath.Join(tmp, "origin.git")
	os.MkdirAll(work, 0o755)
	sh(t, tmp, "init", "-q", "--bare", "-b", "main", origin)
	sh(t, work, "init", "-q", "-b", "main")
	sh(t, work, "remote", "add", "origin", origin)
	sh(t, work, "commit", "-q", "--allow-empty", "-m", "A")
	sh(t, work, "commit", "-q", "--allow-empty", "-m", "B")
	sh(t, work, "branch", "feature")
	sh(t, work, "tag", "v1")
	sh(t, work, "push", "-q", "origin", "main", "feature", "v1")

	g, err := newGit("x")
	if err != nil {
		t.Fatal(err)
	}
	mirror := filepath.Join(tmp, "backup", "o", "r.git")
	if c, _, err := g.syncMirror(ctx, origin, mirror); err != nil || !c {
		t.Fatalf("primo clone: created=%v err=%v", c, err)
	}

	sh(t, work, "commit", "-q", "--allow-empty", "-m", "C")
	sh(t, work, "push", "-q", "origin", "main")
	if _, n, err := g.syncMirror(ctx, origin, mirror); err != nil || n != 0 {
		t.Fatalf("fast-forward: salvati=%d err=%v (attesi 0)", n, err)
	}

	beforeForce := sh(t, mirror, "rev-parse", "main")
	sh(t, work, "reset", "-q", "--hard", "HEAD~2")
	sh(t, work, "commit", "-q", "--allow-empty", "-m", "X")
	sh(t, work, "push", "-q", "-f", "origin", "main")
	sh(t, work, "push", "-q", "origin", ":feature")
	sh(t, work, "tag", "-f", "v1")
	sh(t, work, "push", "-q", "-f", "origin", "v1")

	_, n, err := g.syncMirror(ctx, origin, mirror)
	if err != nil || n != 2 {
		t.Fatalf("force-push: salvati=%d err=%v (attesi 2: main e v1)", n, err)
	}
	if hist := sh(t, mirror, "for-each-ref", "--format=%(objectname)", historyPrefix); !strings.Contains(hist, beforeForce) {
		t.Fatal("il commit precedente al force-push non è nello storico")
	}
	sh(t, mirror, "rev-parse", "--verify", "refs/heads/feature") // fallisce se il branch è sparito
	if _, n, _ := g.syncMirror(ctx, origin, mirror); n != 0 {
		t.Fatalf("giro senza modifiche ha salvato %d ref", n)
	}
}

func TestLockIsExclusive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lock")
	a, _ := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if !tryLock(a) {
		t.Fatal("primo lock fallito")
	}
	// flock è per file aperto: un secondo open nello stesso processo deve risultare bloccato.
	b, _ := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if tryLock(b) {
		t.Fatal("secondo lock riuscito mentre il primo è attivo")
	}
	a.Close()
	if !tryLock(b) {
		t.Fatal("lock non rilasciato alla chiusura")
	}
	b.Close()
}

// Su Linux senza portachiavi (nessun D-Bus) il token deve finire nel file, con permessi 0600.
func TestTokenFileFallback(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("il ripiego su file riguarda Linux senza portachiavi")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent")
	where, err := saveToken("tok123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(where, tokenFile()) {
		t.Fatalf("token salvato %q, atteso nel file", where)
	}
	fi, err := os.Stat(tokenFile())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("permessi del file token: %v %v", fi.Mode().Perm(), err)
	}
	if got := loadSavedToken(); got != "tok123" {
		t.Fatalf("letto %q", got)
	}
	if err := forgetToken(); err != nil || exists(tokenFile()) {
		t.Fatal("logout non ha rimosso il file")
	}
}

func TestAskpass(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(prompt string) string {
		c := exec.Command(exe, prompt)
		c.Env = append(os.Environ(), "GGRABBER_ASKPASS=1", "GGRABBER_TOKEN=segreto")
		out, _ := c.Output()
		return strings.TrimSpace(string(out))
	}
	if u := run("Username for 'https://github.com': "); u != "x-access-token" {
		t.Fatalf("username: %q", u)
	}
	if p := run("Password for 'https://x@github.com': "); p != "segreto" {
		t.Fatalf("password: %q", p)
	}
}

// TestMain permette al binario di test di fare da askpass, come l'eseguibile vero.
func TestMain(m *testing.M) {
	if os.Getenv("GGRABBER_ASKPASS") == "1" {
		askpass()
		return
	}
	os.Exit(m.Run())
}

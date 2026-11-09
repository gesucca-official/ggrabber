// ggrabber: backup locale completo di tutte le repo GitHub raggiungibili da un account.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	version  = "dev"
	clientID = "" // OAuth App client ID, impostato in build con -ldflags "-X main.clientID=..."
)

func main() {
	// Modalità askpass: git invoca questo stesso eseguibile per farsi dare le credenziali.
	if os.Getenv("GGRABBER_ASKPASS") == "1" {
		askpass()
		return
	}

	var (
		dirFlag  = flag.String("dir", "", "cartella di destinazione (default: accanto all'eseguibile)")
		jobs     = flag.Int("jobs", 4, "repo sincronizzate in parallelo")
		noMeta   = flag.Bool("no-meta", false, "non scaricare issue, PR, release ecc.")
		logout   = flag.Bool("logout", false, "dimentica il token salvato ed esci")
		noPause  = flag.Bool("no-pause", false, "non attendere Invio alla fine")
		showVers = flag.Bool("version", false, "mostra la versione")
	)
	flag.Parse()

	if *showVers {
		fmt.Println("ggrabber", version)
		return
	}

	code := run(*dirFlag, *jobs, *noMeta, *logout)
	if !*noPause && isTerminal() {
		fmt.Print("\nPremi Invio per chiudere...")
		readLine()
	}
	os.Exit(code)
}

func run(dirFlag string, jobs int, noMeta, logout bool) int {
	fmt.Printf("ggrabber %s - backup completo delle tue repo GitHub\n\n", version)

	if logout {
		if err := forgetToken(); err != nil {
			fmt.Println("Nessun token salvato da rimuovere.")
		} else {
			fmt.Println("Token rimosso dal portachiavi.")
		}
		return 0
	}

	if _, err := exec.LookPath("git"); err != nil {
		fmt.Println("ERRORE: git non è installato.")
		if runtime.GOOS == "darwin" {
			fmt.Println("Installa con:  xcode-select --install")
		} else {
			fmt.Println("Scaricalo da:  https://git-scm.com/downloads")
		}
		return 1
	}

	root, err := backupRoot(dirFlag)
	if err != nil {
		fmt.Println("ERRORE:", err)
		return 1
	}
	if err := os.MkdirAll(filepath.Join(root, stateDirName), 0o755); err != nil {
		fmt.Println("ERRORE: impossibile creare la cartella di backup:", err)
		return 1
	}
	closeLog := openLog(filepath.Join(root, stateDirName, "ultimo-backup.log"))
	defer closeLog()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	token, user, err := authenticate(ctx)
	if err != nil {
		logf("ERRORE di autenticazione: %v", err)
		return 1
	}
	client := newClient(token)
	g, err := newGit(token)
	if err != nil {
		logf("ERRORE: %v", err)
		return 1
	}

	logf("\nDestinazione: %s", root)
	logf("Recupero l'elenco delle repo raggiungibili da %s...", user.Login)
	repos, err := client.listRepos(ctx)
	if err != nil {
		logf("ERRORE nel recupero delle repo: %v", err)
		return 1
	}
	gists, err := client.listGists(ctx)
	if err != nil {
		logf("Attenzione: impossibile elencare i gist: %v", err)
	}
	sort.Slice(repos, func(i, j int) bool {
		return strings.ToLower(repos[i].FullName) < strings.ToLower(repos[j].FullName)
	})
	logf("Trovate %d repo e %d gist.\n", len(repos), len(gists))

	st := loadState(root)
	handleRenames(root, st, repos)

	start := time.Now()
	type job struct {
		repo *Repo
		gist *Gist
	}
	var all []job
	for _, r := range repos {
		all = append(all, job{repo: r})
	}
	for _, gi := range gists {
		all = append(all, job{gist: gi})
	}

	var (
		mu      sync.Mutex
		done    int
		results []result
	)
	ch := make(chan job)
	var wg sync.WaitGroup
	if jobs < 1 {
		jobs = 1
	}
	for w := 0; w < jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				var res result
				if j.repo != nil {
					res = processRepo(ctx, client, g, root, j.repo, noMeta)
				} else {
					res = processGist(ctx, g, root, j.gist)
				}
				mu.Lock()
				done++
				results = append(results, res)
				logf("[%*d/%d] %s", len(fmt.Sprint(len(all))), done, len(all), res.line())
				mu.Unlock()
			}
		}()
	}
feed:
	for _, j := range all {
		select {
		case ch <- j:
		case <-ctx.Done():
			break feed
		}
	}
	close(ch)
	wg.Wait()

	st.LastRun = time.Now().UTC().Format(time.RFC3339)
	st.Account = user.Login
	if err := st.save(root); err != nil {
		logf("Attenzione: impossibile salvare lo stato: %v", err)
	}

	return summary(ctx, root, results, len(all), time.Since(start))
}

func backupRoot(dirFlag string) (string, error) {
	if dirFlag != "" {
		return filepath.Abs(dirFlag)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Join(filepath.Dir(exe), "github-backup"), nil
}

func summary(ctx context.Context, root string, results []result, total int, elapsed time.Duration) int {
	var created, updated, failed int
	var errs, warns []string
	for _, r := range results {
		switch {
		case r.err != nil:
			failed++
			errs = append(errs, fmt.Sprintf("%s: %v", r.name, r.err))
		case r.created:
			created++
		default:
			updated++
		}
		for _, w := range r.warnings {
			warns = append(warns, fmt.Sprintf("%s: %s", r.name, w))
		}
	}
	sort.Strings(errs)
	sort.Strings(warns)

	logf("\n================ RIEPILOGO ================")
	logf("Nuove:        %d", created)
	logf("Aggiornate:   %d", updated)
	logf("Fallite:      %d", failed)
	if skipped := total - len(results); skipped > 0 {
		logf("Non elaborate (interrotto): %d", skipped)
	}
	logf("Durata:       %s", elapsed.Round(time.Second))
	logf("Spazio usato: %s", humanBytes(dirSize(root)))
	logf("Cartella:     %s", root)
	if len(warns) > 0 {
		logf("\nAvvisi:")
		for _, w := range warns {
			logf("  - %s", w)
		}
	}
	if len(errs) > 0 {
		logf("\nErrori:")
		for _, e := range errs {
			logf("  - %s", e)
		}
	}
	if ctx.Err() != nil {
		logf("\nBackup interrotto. Rilancialo per riprendere da dove si è fermato.")
		return 130
	}
	if failed > 0 {
		return 2
	}
	logf("\nBackup completato.")
	return 0
}

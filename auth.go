package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	keyringService = "ggrabber"
	keyringUser    = "github-token"
	scopes         = "repo read:org gist"
)

func loadSavedToken() string {
	t, err := keyring.Get(keyringService, keyringUser)
	if err != nil {
		return ""
	}
	return t
}

func saveToken(t string) error { return keyring.Set(keyringService, keyringUser, t) }
func forgetToken() error       { return keyring.Delete(keyringService, keyringUser) }

// ghCLIToken riusa il login della GitHub CLI, se presente.
func ghCLIToken() string {
	if _, err := exec.LookPath("gh"); err != nil {
		return ""
	}
	out, err := exec.Command("gh", "auth", "token", "--hostname", "github.com").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// authenticate trova un token valido: prima quelli già disponibili (con conferma
// dell'account), altrimenti fa il login dal browser.
func authenticate(ctx context.Context) (string, *User, error) {
	type source struct {
		name  string
		token func() string
		saved bool
	}
	sources := []source{
		{"variabile d'ambiente GITHUB_TOKEN", func() string { return os.Getenv("GITHUB_TOKEN") }, false},
		{"variabile d'ambiente GH_TOKEN", func() string { return os.Getenv("GH_TOKEN") }, false},
		{"login salvato", loadSavedToken, true},
		{"GitHub CLI (gh)", ghCLIToken, false},
	}
	for _, s := range sources {
		t := s.token()
		if t == "" {
			continue
		}
		u, sc, err := newClient(t).whoami(ctx)
		if err != nil {
			if isStatus(err, 401) && s.saved {
				forgetToken()
				fmt.Println("Il login salvato non è più valido, serve rifarlo.")
			}
			continue
		}
		if !hasRepoScope(sc) {
			continue
		}
		fmt.Printf("Account trovato (%s): %s\n", s.name, describe(u))
		if askYesNo("Vuoi fare il backup di questo account?", true) {
			return t, u, nil
		}
		break
	}

	t, err := interactiveLogin(ctx)
	if err != nil {
		return "", nil, err
	}
	u, sc, err := newClient(t).whoami(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("token non valido: %w", err)
	}
	if !hasRepoScope(sc) {
		fmt.Println("Attenzione: il token non ha il permesso 'repo', le repo private potrebbero mancare.")
	}
	fmt.Printf("\nConnesso come %s\n", describe(u))
	if err := saveToken(t); err != nil {
		fmt.Println("(non sono riuscito a salvare il login nel portachiavi, lo richiederò la prossima volta)")
	} else {
		fmt.Println("Login salvato nel portachiavi di sistema: la prossima volta non serve rifarlo.")
	}
	return t, u, nil
}

func describe(u *User) string {
	if u.Name != "" {
		return fmt.Sprintf("%s (%s)", u.Login, u.Name)
	}
	return u.Login
}

// hasRepoScope: i token "classic" e OAuth espongono gli scope; i fine-grained no (header vuoto).
func hasRepoScope(sc string) bool {
	if strings.TrimSpace(sc) == "" {
		return true
	}
	for _, s := range strings.Split(sc, ",") {
		if strings.TrimSpace(s) == "repo" {
			return true
		}
	}
	return false
}

func interactiveLogin(ctx context.Context) (string, error) {
	if !isTerminal() {
		return "", errors.New("nessun login disponibile e nessun terminale per farlo (imposta GITHUB_TOKEN)")
	}
	if clientID != "" {
		t, err := deviceFlow(ctx)
		if err == nil {
			return t, nil
		}
		fmt.Println("\nLogin dal browser non riuscito:", err)
		fmt.Println("Passo al metodo alternativo con token personale.")
	}
	return patFlow()
}

// deviceFlow: OAuth Device Flow. L'utente conferma nel browser dove è già loggato,
// senza password né 2FA da inserire nel terminale.
func deviceFlow(ctx context.Context) (string, error) {
	var dc struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Error           string `json:"error"`
		ErrorDesc       string `json:"error_description"`
	}
	err := postForm(ctx, "https://github.com/login/device/code",
		url.Values{"client_id": {clientID}, "scope": {scopes}}, &dc)
	if err != nil {
		return "", err
	}
	if dc.Error != "" {
		return "", fmt.Errorf("%s: %s", dc.Error, dc.ErrorDesc)
	}

	fmt.Println("\n--- Login a GitHub ---")
	fmt.Printf("1. Apri:  %s\n", dc.VerificationURI)
	fmt.Printf("2. Inserisci il codice:  %s", dc.UserCode)
	if copyToClipboard(dc.UserCode) {
		fmt.Print("   (già copiato negli appunti, basta incollarlo)")
	}
	fmt.Println()
	fmt.Println("3. Clicca \"Authorize\".")
	fmt.Println()
	if askYesNo("Aprire la pagina nel browser predefinito? (rispondi n se la apri tu in un altro browser)", true) {
		openBrowser(dc.VerificationURI)
	}
	fmt.Println("In attesa della conferma nel browser...")

	interval := time.Duration(max(dc.Interval, 5)) * time.Second
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		if err := sleepCtx(ctx, interval); err != nil {
			return "", err
		}
		var tr struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
			ErrorDesc   string `json:"error_description"`
			Interval    int    `json:"interval"`
		}
		err := postForm(ctx, "https://github.com/login/oauth/access_token", url.Values{
			"client_id":   {clientID},
			"device_code": {dc.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}, &tr)
		if err != nil {
			continue // errore di rete temporaneo: riprova
		}
		switch tr.Error {
		case "":
			if tr.AccessToken != "" {
				return tr.AccessToken, nil
			}
		case "authorization_pending":
		case "slow_down":
			if tr.Interval > 0 {
				interval = time.Duration(tr.Interval) * time.Second
			} else {
				interval += 5 * time.Second
			}
		case "access_denied":
			return "", errors.New("autorizzazione negata nel browser")
		case "expired_token":
			return "", errors.New("il codice è scaduto")
		default:
			return "", fmt.Errorf("%s: %s", tr.Error, tr.ErrorDesc)
		}
	}
	return "", errors.New("tempo scaduto in attesa della conferma")
}

func postForm(ctx context.Context, u string, form url.Values, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ggrabber/"+version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

// patFlow: apre la pagina di creazione token già compilata; basta cliccare "Generate" e incollare.
func patFlow() (string, error) {
	u := "https://github.com/settings/tokens/new?scopes=" +
		url.QueryEscape(strings.ReplaceAll(scopes, " ", ",")) +
		"&description=" + url.QueryEscape("ggrabber backup")
	fmt.Println("\n--- Login a GitHub con token personale ---")
	fmt.Println("1. Apri questa pagina nel browser dove sei già loggato:")
	fmt.Println("   " + u)
	fmt.Println("2. I permessi sono già selezionati: scegli una scadenza, scorri in fondo e clicca \"Generate token\".")
	fmt.Println("3. Copia il token e incollalo qui sotto.")
	fmt.Println()
	if askYesNo("Aprire la pagina nel browser predefinito?", true) {
		openBrowser(u)
	}
	for i := 0; i < 3; i++ {
		fmt.Print("Token (non verrà mostrato): ")
		t := readSecret()
		if t != "" {
			return t, nil
		}
	}
	return "", errors.New("nessun token inserito")
}

// askpass viene eseguito quando git chiede le credenziali (GIT_ASKPASS).
func askpass() {
	prompt := strings.ToLower(strings.Join(os.Args[1:], " "))
	if strings.Contains(prompt, "username") {
		fmt.Println("x-access-token")
		return
	}
	fmt.Println(os.Getenv("GGRABBER_TOKEN"))
}

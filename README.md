# ggrabber

Backup locale completo di **tutte** le repo GitHub raggiungibili da un account: le tue, quelle delle
organizzazioni di cui fai parte e quelle dove sei collaboratore, private comprese. Include anche i gist.

Per usarlo: doppio clic sull'eseguibile. Si apre un terminale e il programma:

1. fa il login a GitHub dal browser in cui sei già loggato (niente password o 2FA da digitare);
2. scarica o aggiorna tutto in `github-backup/`, accanto all'eseguibile;
3. mostra un riepilogo e aspetta Invio prima di chiudersi.

Rilanciandolo scarica solo le novità.

## Cosa viene salvato

```
github-backup/
  <owner>/<repo>.git        mirror git completo: tutti i branch, tag e refs delle PR
  <owner>/<repo>.wiki.git   wiki, se esiste
  <owner>/<repo>.meta/      repo.json, labels, milestones,
                            issues/<n>.json (issue/PR con commenti, review, commenti di review),
                            releases/ (releases.json + tutti gli asset)
  _gists/<id>.git           gist, anche quelli segreti
  _ggrabber/                stato interno e log dell'ultimo backup
```

- I mirror sono repo git "bare": git comprime già tutto, quindi zipparli non farebbe risparmiare spazio.
  Per avere i file di una repo basta clonare il mirror: `git clone github-backup/owner/repo.git`.
- **Il backup non perde mai nulla di ciò che ha già salvato**:
  - le repo e i branch eliminati da GitHub restano nel backup;
  - se un branch o un tag viene riscritto (force-push, tag spostato), la versione precedente è
    conservata in `refs/ggrabber-history/<data>/…` e si vede con `git for-each-ref refs/ggrabber-history`;
  - le repo rinominate o trasferite vengono spostate nella nuova cartella, senza riscaricarle.
- Rilanciarlo è sicuro: aggiorna solo ciò che è cambiato. Due copie avviate insieme sulla stessa cartella
  non si pestano i piedi, perché la seconda si ferma subito.
- Se manca `git-lfs`, i file LFS non vengono salvati (compare un avviso).

## Login

In ordine di preferenza, il programma usa:
1. il login salvato da un'esecuzione precedente: nel portachiavi del sistema oppure, su Linux senza
   desktop (es. NAS o server), nel file `~/.config/ggrabber/token`, leggibile solo dal tuo utente;
2. la GitHub CLI (`gh`), se installata e loggata;
3. il **login dal browser** (OAuth Device Flow): apri la pagina, incolli il codice (è già negli
   appunti) e clicchi Authorize;
4. in alternativa, un **token personale**: si apre la pagina di creazione con i permessi già selezionati,
   clicchi Generate e lo incolli.

Prima di partire chiede sempre conferma dell'account. Per cambiarlo rispondi `n`; per cancellare il
login salvato usa `ggrabber --logout`.

### OAuth App

Il login dal browser usa l'OAuth App "ggrabber", il cui Client ID è già incluso in `build.sh`.
Per usarne un'altra: GitHub → Settings → Developer settings → OAuth Apps → New OAuth App, spunta
**Enable Device Flow** e compila con `GGRABBER_CLIENT_ID=<client id> ./build.sh`.

Le organizzazioni con "OAuth app access restrictions" nascondono le proprie repo private alle OAuth App
non approvate. In quel caso un amministratore deve approvare l'app, oppure usa il token personale.

## Primo avvio

- **macOS**: il binario non è firmato. Al primo avvio fai tasto destro → Apri → Apri,
  oppure esegui `xattr -d com.apple.quarantine ggrabber-macos`.
- **Windows**: SmartScreen può avvisare: "Ulteriori informazioni" → "Esegui comunque".
- **Linux**: estrai con `tar xzf ggrabber-linux-<arch>.tar.gz` (`amd64` per i PC, `arm64` per
  Raspberry Pi e molti NAS). Aperto con doppio clic dal file manager si rilancia da solo in un
  terminale; altrimenti avvialo da terminale con `./ggrabber-linux-amd64`. Senza desktop (es. via SSH
  sul NAS) il login dal browser funziona lo stesso: apri l'indirizzo da qualunque altro dispositivo.
- Serve `git` installato (su macOS: `xcode-select --install`; su Linux dal gestore pacchetti).

## Opzioni

```
--dir <cartella>   destinazione diversa
--jobs <n>         repo in parallelo (default 4)
--no-meta          solo git, niente issue/PR/release
--logout           dimentica il login salvato
--no-pause         non aspettare Invio alla fine (per uso schedulato)
```

Per un uso non interattivo si può anche impostare `GITHUB_TOKEN`.

## Build

```bash
GGRABBER_CLIENT_ID=<client id> ./build.sh
```

Produce in `dist/` l'exe Windows, i binari macOS (uniti in un binario universale se compilato su un
Mac) e i binari Linux amd64 e arm64. Con un tag `v*` la GitHub Action pubblica una release con tutti.

### Logo

`tools/icon` disegna il logo e genera `assets/icon.png` e `assets/icon.ico`. Su Windows l'icona entra
nell'exe tramite `rsrc_windows_amd64.syso`. Dopo aver modificato il logo, rigeneralo con:

```bash
go run ./tools/icon && go-winres make --arch amd64
```

Un eseguibile macOS o Linux "nudo" non può avere un'icona propria.

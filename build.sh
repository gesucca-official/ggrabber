#!/usr/bin/env bash
# Compila gli eseguibili in dist/.
#   ./build.sh
# Il Client ID dell'OAuth App di ggrabber è incluso; GGRABBER_CLIENT_ID permette di usarne un altro.
# Icona Windows: rsrc_windows_amd64.syso (versionato). Per rigenerarla dopo aver cambiato il logo:
#   go run ./tools/icon && go-winres make --arch amd64
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${VERSION:-$(date +%Y.%m.%d)}"
LDFLAGS="-s -w -X main.version=${VERSION} -X main.clientID=${GGRABBER_CLIENT_ID:-Ov23li6yB0hcWkldHnba}"
export CGO_ENABLED=0

mkdir -p dist
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o dist/ggrabber-windows.exe .
GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o dist/ggrabber-macos-arm64 .
GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o dist/ggrabber-macos-amd64 .

# Su macOS unisce le due architetture in un unico binario universale.
if command -v lipo >/dev/null 2>&1; then
  lipo -create -output dist/ggrabber-macos dist/ggrabber-macos-arm64 dist/ggrabber-macos-amd64
  rm dist/ggrabber-macos-arm64 dist/ggrabber-macos-amd64
fi
ls -lh dist

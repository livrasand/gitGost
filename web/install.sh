#!/usr/bin/env bash
#
# Instalador de git-gost (extensión Git de gitGost).
# Uso:
#   curl -fsSL https://gitgost.fly.dev/install | bash
#   curl -fsSL https://gitgost.fly.dev/install | GITGOST_SERVER=https://mi.instancia.com bash
#
set -euo pipefail

REPO="livrasand/gitGost"
DEFAULT_SERVER="https://gitgost.fly.dev"
tmp=""

# Permite apuntar el cliente a otra instancia de gitGost.
: "${GITGOST_SERVER:=${DEFAULT_SERVER}}"

say() {
  printf 'git-gost-install: %s\n' "$*"
}

err() {
  printf 'git-gost-install: %s\n' "$*" >&2
}

detect_os() {
  local raw
  raw=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$raw" in
    linux*)          printf 'linux' ;;
    darwin*)         printf 'darwin' ;;
    mingw*|msys*|cygwin*|windows*) printf 'windows' ;;
    *)
      err "sistema operativo no soportado: $raw"
      exit 1 ;;
  esac
}

detect_arch() {
  local raw
  raw=$(uname -m | tr '[:upper:]' '[:lower:]')
  case "$raw" in
    x86_64|amd64)   printf 'amd64' ;;
    arm64|aarch64)  printf 'arm64' ;;
    *)
      err "arquitectura no soportada: $raw"
      exit 1 ;;
  esac
}

find_asset_url() {
  local asset url
  asset=$1

  # Usamos la API de releases para encontrar un asset con nombre exacto.
  # Soporta tags con '/' (ej. gren/v0.1.0) y prioriza el release más reciente.
  if command -v python3 >/dev/null 2>&1; then
    url=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases?per_page=20" \
          | python3 -c "
import json, sys, os
asset = sys.argv[1]
pinned = os.environ.get('GITGOST_VERSION', '')
try:
    releases = json.load(sys.stdin)
except Exception:
    sys.exit(1)
for rel in releases:
    if pinned and rel.get('tag_name') != pinned:
        continue
    for a in rel.get('assets', []):
        if a.get('name') == asset:
            print(a.get('browser_download_url', ''))
            sys.exit(0)
sys.exit(1)
" "$asset")
    if [ -n "$url" ]; then
      printf '%s' "$url"
      return 0
    fi
  elif command -v jq >/dev/null 2>&1; then
    url=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases?per_page=20" \
          | jq -r --arg asset "$asset" --arg pinned "${GITGOST_VERSION:-}" '
            .[] | select(.tag_name == (if $pinned == "" then .tag_name else $pinned end))
            | .assets[] | select(.name == $asset) | .browser_download_url
            ' | head -n1)
    if [ -n "$url" ] && [ "$url" != "null" ]; then
      printf '%s' "$url"
      return 0
    fi
  fi

  err "no se pudo localizar ${asset} en los releases de ${REPO}"
  err "asegúrate de tener python3 o jq instalados, o descarga manualmente desde:"
  err "  https://github.com/${REPO}/releases"
  return 1
}

asset_name() {
  local os arch ext
  os=$1
  arch=$2
  ext=""
  if [ "$os" = "windows" ]; then
    ext=".exe"
  fi
  printf 'git-gost-%s-%s%s' "$os" "$arch" "$ext"
}

checksum_cmd() {
  if command -v sha256sum >/dev/null 2>&1; then
    printf 'sha256sum'
  elif command -v shasum >/dev/null 2>&1; then
    printf 'shasum -a 256'
  else
    printf ''
  fi
}

main() {
  local os arch asset url sha_url expected actual hash_cmd

  os=$(detect_os)
  arch=$(detect_arch)
  say "plataforma detectada: ${os}/${arch}"

  asset=$(asset_name "$os" "$arch")
  url=$(find_asset_url "$asset")
  sha_url="${url}.sha256"

  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT

  say "descargando ${asset}..."
  curl -fsSL --retry 3 --retry-delay 2 "$url" -o "$tmp/git-gost"

  # Si existe el archivo .sha256 en el release, verificamos la integridad.
  if curl -fsSL --retry 3 --retry-delay 2 "$sha_url" -o "$tmp/${asset}.sha256" 2>/dev/null; then
    expected=$(head -n1 "$tmp/${asset}.sha256" | awk '{print $1}')
    if [ -n "$expected" ]; then
      hash_cmd=$(checksum_cmd)
      if [ -n "$hash_cmd" ]; then
        actual=$($hash_cmd "$tmp/git-gost" | awk '{print $1}')
        if [ "$expected" != "$actual" ]; then
          err "verificación SHA-256 fallida (esperado: ${expected}, obtenido: ${actual})"
          exit 1
        fi
        say "verificación SHA-256 correcta"
      else
        say "aviso: no se encontró sha256sum/shasum; omitiendo verificación"
      fi
    fi
  fi

  chmod +x "$tmp/git-gost"

  # El comando install de git-gost se encarga de copiar el binario a
  # ~/.local/bin/git-gost, preparar ~/.gitgost y añadir el directorio al PATH.
  say "instalando git-gost..."
  GITGOST_SERVER="${GITGOST_SERVER}" "$tmp/git-gost" install

  if [ -n "${GITGOST_SERVER}" ] && [ "${GITGOST_SERVER}" != "${DEFAULT_SERVER}" ]; then
    say "cliente configurado para usar servidor: ${GITGOST_SERVER}"
    say "para que sea persistente, añade a tu shell:"
    printf '  export GITGOST_SERVER=%q\n' "${GITGOST_SERVER}"
  fi

  say "instalación completada. Recarga tu shell o ejecuta:"
  say "  git gost version"
}

main "$@"

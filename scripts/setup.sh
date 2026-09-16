#!/usr/bin/env bash
set -euo pipefail
CURRENT_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd -- "$CURRENT_ROOT"
missing=()
for tool in go cargo node pnpm python3 cc make; do
  command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
done
if (( ${#missing[@]} )); then
  printf 'Missing build tools: %s\n' "${missing[*]}" >&2
  printf 'On macOS, install Command Line Tools (xcode-select --install), then:\n  brew install go rust node pnpm python\n' >&2
  exit 1
fi
printf 'Building Current for %s / %s…\n' "$(uname -s)" "$(uname -m)"
make build
printf '\nReady. Run ./current tui in Kitty, or ./current web for the browser.\n'

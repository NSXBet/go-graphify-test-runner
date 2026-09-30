#!/usr/bin/env sh
# Install smart-test-runner from GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/NSXBet/go-smart-test-runner/main/install.sh | sh
#
# Honours:
#   INSTALL_DIR   where to place the binary   (default: /usr/local/bin, else ~/.local/bin)
#   VERSION       pin a release tag            (default: latest)
#   BASE_URL      override the release root    (default: https://github.com/$REPO/releases; for mirrors/tests)
#
# Supports linux and darwin on amd64 and arm64. Windows users should download
# the .zip from the releases page (or use `go install`), which this POSIX
# shell script cannot handle.
set -eu

REPO="NSXBet/go-smart-test-runner"
BIN="smart-test-runner"

info() { printf '%s\n' "$*" >&2; }
fail() { printf 'install: %s\n' "$*" >&2; exit 1; }

need() {
	command -v "$1" >/dev/null 2>&1 || fail "'$1' is required but not found in PATH"
}

need uname
need curl
need tar

# --- platform detection -----------------------------------------------------
os="$(uname -s)"
case "$os" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "unsupported OS '$os'. On Windows, download the .zip from https://github.com/$REPO/releases or run: go install github.com/$REPO@latest" ;;
esac

arch="$(uname -m)"
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail "unsupported architecture '$arch' (supported: amd64, arm64)" ;;
esac

# --- version resolution -----------------------------------------------------
version="${VERSION:-}"
if [ -z "$version" ]; then
	# Resolve the latest tag via the releases/latest redirect; avoids the
	# GitHub API (and its rate limits / token requirement).
	releases_root="${BASE_URL:-https://github.com/$REPO/releases}"
	latest_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$releases_root/latest")" ||
		fail "could not resolve the latest release (set VERSION to pin one)"
	version="${latest_url##*/}"
	[ -n "$version" ] || fail "could not parse the latest version from '$latest_url'"
fi

# Normalise a bare version (1.2.3) to the tag form (v1.2.3).
case "$version" in
v*) ;;
*) version="v$version" ;;
esac

# --- install directory ------------------------------------------------------
dir="${INSTALL_DIR:-}"
if [ -z "$dir" ]; then
	if [ -w /usr/local/bin ] 2>/dev/null; then
		dir=/usr/local/bin
	else
		dir="$HOME/.local/bin"
	fi
fi
mkdir -p "$dir" || fail "cannot create install directory '$dir'"

# --- download and verify ----------------------------------------------------
# The release *tag* keeps the leading v (v1.2.3); the archive *name* uses the
# bare version (1.2.3) — that is goreleaser's {{ .Version }}.
bare="${version#v}"
archive="${BIN}_${bare}_${os}_${arch}.tar.gz"
base="${BASE_URL:-https://github.com/$REPO/releases}/download/$version"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

info "Downloading $archive ($version)..."
curl -fsSL -o "$tmp/$archive" "$base/$archive" || fail "download failed: $base/$archive"

# Checksums are best-effort: a missing checksums.txt (older release) warns but
# does not abort; a mismatch is fatal.
if curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" 2>/dev/null; then
	expected="$(grep " $archive\$" "$tmp/checksums.txt" | awk '{print $1}')"
	if [ -n "$expected" ]; then
		actual="$(sha256sum "$tmp/$archive" 2>/dev/null | awk '{print $1}')"
		[ -n "$actual" ] || actual="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
		[ "$expected" = "$actual" ] || fail "checksum mismatch for $archive (expected $expected, got $actual)"
		info "Checksum verified."
	else
		info "WARNING: $archive not listed in checksums.txt; skipping verification."
	fi
else
	info "WARNING: checksums.txt unavailable; skipping verification."
fi

tar -xzf "$tmp/$archive" -C "$tmp" || fail "could not extract $archive"
[ -f "$tmp/$BIN" ] || fail "archive did not contain '$BIN'"

install -m 0755 "$tmp/$BIN" "$dir/$BIN" 2>/dev/null || {
	cp "$tmp/$BIN" "$dir/$BIN" && chmod 0755 "$dir/$BIN"
} || fail "could not install to '$dir'"

info "Installed $BIN $version to $dir/$BIN"

case ":$PATH:" in
*":$dir:"*) ;;
*) info "NOTE: '$dir' is not on your PATH — add it, e.g.: export PATH=\"$dir:\$PATH\"" ;;
esac

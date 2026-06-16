#!/bin/sh
# install.sh — install the go-stashapp `stash` CLI on Linux or macOS.
#
# Usage (the headline path):
#
#   curl -sSL https://raw.githubusercontent.com/lightning-rider-999/go-stashapp/main/install.sh | sh
#
# This downloads the correct release archive for your OS/arch from GitHub
# Releases, verifies its sha256 against the published checksums.txt, extracts the
# `stash` binary, and installs it into a directory on (or addable to) your PATH.
#
# Environment overrides:
#
#   VERSION=v1.2.3   pin a specific release tag (default: the latest release)
#   INSTALL_DIR=...  install into this directory (default: /usr/local/bin, with a
#                    sudo or ~/.local/bin fallback when it is not writable)
#
# This is a curl|sh installer, so the shebang above is ignored and the script is
# interpreted by whatever `sh` is on the user's box. It is therefore strict
# POSIX sh: no bashisms (no [[ ]], no arrays), `set -eu`, everything quoted.
#
# Windows is not a curl|sh target. On Windows use:
#   go install github.com/lightning-rider-999/go-stashapp/cmd/stash@latest
# or download a .zip from the Releases page.

set -eu

# --- configuration -----------------------------------------------------------

OWNER="lightning-rider-999"
REPO="go-stashapp"
BINARY="stash"
API_BASE="https://api.github.com/repos/${OWNER}/${REPO}"

# --- output helpers ----------------------------------------------------------

# info prints a progress line to stderr so it never pollutes any captured stdout.
info() {
	printf '==> %s\n' "$*" >&2
}

# warn prints a non-fatal notice to stderr.
warn() {
	printf 'warning: %s\n' "$*" >&2
}

# die prints an error to stderr and exits non-zero.
die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

# --- platform detection ------------------------------------------------------

# detect_os maps `uname -s` to the GoReleaser OS token (linux|darwin). Anything
# else (including Windows/MINGW/MSYS/Cygwin) is unsupported by this installer; it
# prints guidance and returns non-zero so the caller can exit cleanly.
detect_os() {
	os_raw="$(uname -s)"
	case "$os_raw" in
	Linux) printf 'linux\n' ;;
	Darwin) printf 'darwin\n' ;;
	MINGW* | MSYS* | CYGWIN* | Windows_NT)
		cat >&2 <<-EOF
			error: Windows is not supported by this curl|sh installer (detected: ${os_raw}).
			       Install with Go instead:
			         go install github.com/${OWNER}/${REPO}/cmd/${BINARY}@latest
			       or download a .zip from:
			         https://github.com/${OWNER}/${REPO}/releases
		EOF
		return 1
		;;
	*)
		cat >&2 <<-EOF
			error: unsupported operating system: ${os_raw}
			       This installer supports Linux and macOS only. Install with Go instead:
			         go install github.com/${OWNER}/${REPO}/cmd/${BINARY}@latest
			       or download from:
			         https://github.com/${OWNER}/${REPO}/releases
		EOF
		return 1
		;;
	esac
}

# detect_arch maps `uname -m` to the GoReleaser arch token (amd64|arm64).
detect_arch() {
	arch_raw="$(uname -m)"
	case "$arch_raw" in
	x86_64 | amd64) printf 'amd64\n' ;;
	aarch64 | arm64) printf 'arm64\n' ;;
	*)
		cat >&2 <<-EOF
			error: unsupported architecture: ${arch_raw}
			       This installer supports amd64 (x86_64) and arm64 (aarch64) only.
			       Install with Go instead:
			         go install github.com/${OWNER}/${REPO}/cmd/${BINARY}@latest
			       or download from:
			         https://github.com/${OWNER}/${REPO}/releases
		EOF
		return 1
		;;
	esac
}

# --- download helpers --------------------------------------------------------

# have checks whether a command exists on PATH.
have() {
	command -v "$1" >/dev/null 2>&1
}

# fetch downloads $1 and writes the body to stdout, using curl or wget.
# It exits non-zero on any HTTP/transport error so a 404 can never be mistaken
# for a body.
fetch() {
	url="$1"
	if have curl; then
		curl -fsSL "$url"
	elif have wget; then
		wget -qO- "$url"
	else
		die "need either curl or wget to download files, found neither"
	fi
}

# fetch_to downloads $1 to the file path $2.
fetch_to() {
	url="$1"
	dest="$2"
	if have curl; then
		curl -fsSL -o "$dest" "$url"
	elif have wget; then
		wget -q -O "$dest" "$url"
	else
		die "need either curl or wget to download files, found neither"
	fi
}

# --- release resolution ------------------------------------------------------

# asset_url_for prints the browser_download_url of the asset whose name ends with
# the given suffix, parsed from a GitHub release JSON blob on stdin. It matches
# by suffix (never by reconstructing a filename from a version string), which
# sidesteps the leading-`v` ambiguity in tags vs. GoReleaser's .Version segment.
#
# The GitHub release JSON lists, per asset, both a "name" and a
# "browser_download_url". We isolate every browser_download_url, then keep the
# one whose URL path ends with $suffix. URLs cannot contain unescaped quotes, so
# a quote-delimited grep is safe and jq is not required.
#
# $1: the filename suffix to match, e.g. _linux_amd64.tar.gz or /checksums.txt
asset_url_for() {
	suffix="$1"
	# 1. Pull out every "browser_download_url": "..." value, one URL per line.
	# 2. Keep the URL ending in the suffix.
	# 3. Take the first match (there is exactly one per asset name).
	grep -o '"browser_download_url"[[:space:]]*:[[:space:]]*"[^"]*"' |
		sed -e 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"//' -e 's/"$//' |
		grep -- "${suffix}\$" |
		head -n 1
}

# resolve_release_json prints the GitHub release JSON for the requested version
# (or the latest release when VERSION is unset/empty).
resolve_release_json() {
	if [ -n "${VERSION:-}" ]; then
		info "Resolving release ${VERSION}"
		fetch "${API_BASE}/releases/tags/${VERSION}"
	else
		info "Resolving the latest release"
		fetch "${API_BASE}/releases/latest"
	fi
}

# --- checksum verification ---------------------------------------------------

# sha256_of prints the lowercase sha256 hex digest of the file $1 using whichever
# tool is available (GNU coreutils sha256sum, or macOS/BSD `shasum -a 256`).
sha256_of() {
	file="$1"
	if have sha256sum; then
		sha256sum "$file" | cut -d ' ' -f 1
	elif have shasum; then
		shasum -a 256 "$file" | cut -d ' ' -f 1
	else
		die "need sha256sum or shasum to verify the download, found neither"
	fi
}

# expected_sum_for prints the expected sha256 for the asset named $2, read from
# the checksums.txt file $1. checksums.txt is GoReleaser's standard
# "<hex>  <filename>" format, one asset per line.
expected_sum_for() {
	checksums_file="$1"
	asset_name="$2"
	# Anchor on a trailing whitespace + exact filename so e.g. foo.tar.gz cannot
	# match foo.tar.gz.sig. Print only the hex digest (first field).
	awk -v name="$asset_name" '$2 == name { print $1; exit }' "$checksums_file"
}

# verify_checksum aborts unless the sha256 of file $1 equals the expected digest
# for asset name $2 listed in checksums.txt $3.
verify_checksum() {
	archive="$1"
	asset_name="$2"
	checksums_file="$3"

	expected="$(expected_sum_for "$checksums_file" "$asset_name")"
	[ -n "$expected" ] ||
		die "no checksum for ${asset_name} in checksums.txt — refusing to install an unverifiable archive"

	actual="$(sha256_of "$archive")"
	if [ "$expected" != "$actual" ]; then
		cat >&2 <<-EOF
			error: CHECKSUM MISMATCH for ${asset_name}
			         expected: ${expected}
			         actual:   ${actual}
			       Refusing to install. The download may be corrupt or tampered with.
		EOF
		exit 1
	fi
	info "Checksum OK (sha256 ${actual})"
}

# --- install destination -----------------------------------------------------

# choose_install_dir decides where to put the binary and prints the directory.
# Honors INSTALL_DIR; otherwise prefers /usr/local/bin (directly if writable,
# via sudo if available), falling back to ~/.local/bin. It sets the global
# USE_SUDO=1 when the install must go through sudo.
USE_SUDO=0
choose_install_dir() {
	if [ -n "${INSTALL_DIR:-}" ]; then
		printf '%s\n' "$INSTALL_DIR"
		return 0
	fi

	default_dir="/usr/local/bin"
	if [ -d "$default_dir" ] && [ -w "$default_dir" ]; then
		printf '%s\n' "$default_dir"
		return 0
	fi
	# Directory might not exist yet but its parent could be writable by us.
	if [ ! -e "$default_dir" ] && [ -w "$(dirname "$default_dir")" ]; then
		printf '%s\n' "$default_dir"
		return 0
	fi
	if have sudo; then
		USE_SUDO=1
		printf '%s\n' "$default_dir"
		return 0
	fi

	printf '%s\n' "${HOME}/.local/bin"
}

# install_binary copies the built binary $1 to directory $2 as an executable,
# using sudo when USE_SUDO=1. Prints the final installed path on stdout.
install_binary() {
	src="$1"
	dir="$2"
	dest="${dir}/${BINARY}"

	if [ "$USE_SUDO" = "1" ]; then
		info "Installing into ${dir} via sudo (you may be prompted for your password)"
		sudo mkdir -p "$dir"
		sudo install -m 0755 "$src" "$dest" 2>/dev/null ||
			{ sudo cp "$src" "$dest" && sudo chmod 0755 "$dest"; }
	else
		mkdir -p "$dir"
		install -m 0755 "$src" "$dest" 2>/dev/null ||
			{ cp "$src" "$dest" && chmod 0755 "$dest"; }
	fi

	printf '%s\n' "$dest"
}

# on_path reports whether directory $1 is a component of $PATH.
on_path() {
	dir="$1"
	case ":${PATH}:" in
	*":${dir}:"*) return 0 ;;
	*) return 1 ;;
	esac
}

# --- main flow ---------------------------------------------------------------

main() {
	os="$(detect_os)" || exit 1
	arch="$(detect_arch)" || exit 1
	info "Detected platform: ${os}/${arch}"

	release_json="$(resolve_release_json)" ||
		die "could not fetch release metadata from GitHub (is the network up, and does the release exist?)"

	# Suffix match against the GoReleaser archive name
	# stash_<version>_<os>_<arch>.tar.gz — by suffix, not reconstruction.
	archive_suffix="_${os}_${arch}.tar.gz"
	archive_url="$(printf '%s' "$release_json" | asset_url_for "$archive_suffix")"
	[ -n "$archive_url" ] ||
		die "no release asset matching *${archive_suffix} was found for this release"

	checksums_url="$(printf '%s' "$release_json" | asset_url_for "/checksums.txt")"
	[ -n "$checksums_url" ] ||
		die "no checksums.txt asset found in the release — cannot verify the download"

	# The asset filename is the last path segment of the archive URL.
	archive_name="${archive_url##*/}"

	# Temp workspace, cleaned up unconditionally on exit.
	tmpdir="$(mktemp -d 2>/dev/null || mktemp -d -t stash-install)"
	[ -n "$tmpdir" ] && [ -d "$tmpdir" ] ||
		die "could not create a temporary directory"
	trap 'rm -rf "$tmpdir"' EXIT INT HUP TERM

	info "Downloading ${archive_name}"
	fetch_to "$archive_url" "${tmpdir}/${archive_name}" ||
		die "failed to download ${archive_url}"

	info "Downloading checksums.txt"
	fetch_to "$checksums_url" "${tmpdir}/checksums.txt" ||
		die "failed to download ${checksums_url}"

	verify_checksum "${tmpdir}/${archive_name}" "$archive_name" "${tmpdir}/checksums.txt"

	info "Extracting"
	tar -xzf "${tmpdir}/${archive_name}" -C "$tmpdir" ||
		die "failed to extract ${archive_name}"

	# Locate the binary inside the extracted tree (top level per the archive
	# layout, but search to be robust to any future nesting).
	binpath="${tmpdir}/${BINARY}"
	if [ ! -f "$binpath" ]; then
		binpath="$(find "$tmpdir" -type f -name "$BINARY" 2>/dev/null | head -n 1)"
	fi
	[ -n "$binpath" ] && [ -f "$binpath" ] ||
		die "the ${BINARY} binary was not found inside the archive"
	chmod +x "$binpath" 2>/dev/null || true

	install_dir="$(choose_install_dir)"
	installed_path="$(install_binary "$binpath" "$install_dir")"

	info "Installed ${BINARY} to ${installed_path}"

	if ! on_path "$install_dir"; then
		cat >&2 <<-EOF
			==> Note: ${install_dir} is not on your PATH.
			    Add it for this and future shells, e.g.:
			      export PATH="${install_dir}:\$PATH"
			    (put that line in your ~/.profile, ~/.bashrc, or ~/.zshrc).
		EOF
	fi

	# Confirm the install by running the binary's version flag. cobra registers
	# --version because cmd/stash sets the root command's Version field.
	info "Verifying installation:"
	if "$installed_path" --version >&2 2>/dev/null; then
		:
	else
		warn "installed, but '${BINARY} --version' did not run cleanly; check ${installed_path} manually"
	fi

	info "Done. Run '${BINARY} --help' to get started."
}

# Self-test hook: when STASH_INSTALL_SELFTEST is set, the functions above are
# defined but main() is not run, so a test harness can source this file and call
# detect_os / detect_arch / asset_url_for / expected_sum_for / sha256_of in
# isolation. In normal use the variable is unset and main runs.
if [ -z "${STASH_INSTALL_SELFTEST:-}" ]; then
	main "$@"
fi

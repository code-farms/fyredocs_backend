#!/usr/bin/env bash
# Fingerprint of the Go workspace "blueprint" that fyredocs-go-builder bakes in.
#
# The base image snapshots go.work + every service go.mod/go.sum at build time and
# pre-populates /go/pkg/mod and the warm go-build cache from them. Once any of those
# files change, the image is STALE: service builds copy fresh source on top of a base
# whose workspace/module cache no longer matches, and `go build` fails with confusing
# errors like
#
#   go: module ../shared listed in go.work file requires go >= 1.25.7,
#       but go.work lists go 1.25.0
#
# So we hash exactly those files and stamp the result onto the image as the
# `fyredocs.gomod-hash` label. deploy.sh compares the label to the current hash and
# rebuilds the base when they diverge — nobody has to remember to do it by hand.
#
# This file is SOURCED (not executed) by deploy.sh and build-base-image.sh so both
# compute the hash identically. Callers must already be cd'd to the repo root
# (fyredocs_backend/); both do this via their ROOT_DIR resolution.
#
# Usage:  source deployment/scripts/gomod-fingerprint.sh
#         hash=$(gomod_fingerprint)

# Pick a sha256 tool: coreutils on Linux/CI, BSD `shasum` on macOS.
_gomod_sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum
    else
        shasum -a 256
    fi
}

# Emit a stable sha256 over the dependency blueprint. The filename is written into
# the stream alongside each file's contents, so adding or removing a module changes
# the hash even when no existing file's contents did.
# LC_ALL=C pins glob collation so the file order — and therefore the hash — is the
# same on a dev Mac and on a Linux CI box. Without it a locale difference alone would
# look like a stale base image.
# A subshell, so the LC_ALL override never leaks back into the calling script.
#
# The per-module files are listed with `find`, not a `*/go.mod` glob: an unmatched
# glob is a fatal error under zsh and expands to a literal under bash, so a glob
# would make the hash depend on which shell sourced this. -mindepth/-maxdepth 2
# also keeps the walk to one level, so it never descends into node_modules/ or
# vendor trees.
gomod_fingerprint() {
    (
        LC_ALL=C
        {
            for f in go.work go.work.sum; do
                [ -f "$f" ] || continue
                printf '### %s\n' "$f"
                cat "$f"
            done

            find . -mindepth 2 -maxdepth 2 \( -name go.mod -o -name go.sum \) -print |
                sort |
                while IFS= read -r f; do
                    printf '### %s\n' "${f#./}"
                    cat "$f"
                done
        }
    ) | _gomod_sha256 | cut -d' ' -f1
}

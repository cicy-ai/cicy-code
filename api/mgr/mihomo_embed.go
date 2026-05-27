package main

import _ "embed"

// embeddedMihomoGz is the gzip-compressed mihomo proxy binary for THIS build's
// platform, baked in at build time (build.sh build_one fetches the matching
// mihomo-<os>-<arch> and gzips it to mihomo_bin.gz before `go build`). The
// committed file is a ~20-byte placeholder so plain `go build`/`go test`
// compiles; release builds overwrite it with the real ~13 MB gz.
//
// Embedding avoids the slow, blocking, network-dependent `cicy-mihomo install`
// download at first startup (it pulled ~29 MB from gh-proxy.com before the API
// could bind :8008). ensureMihomoBinaryInstalled() decompresses this straight
// to ~/.local/bin/mihomo when the blob is real, falling back to the download
// only when it's the placeholder.
//
//go:embed mihomo_bin.gz
var embeddedMihomoGz []byte

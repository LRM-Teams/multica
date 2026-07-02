// Package stickerimg embeds the sticker image bytes and serves them by
// filename. It is split out from the stickers package so the image payload
// (several MB) embeds only into binaries that actually serve images — the
// server — and not into the CLI, which imports stickers for search but never
// needs the bytes.
//
// Filenames here are the File values in the stickers catalog; the two are kept
// 1:1 (verified by the stickers package tests).
package stickerimg

import (
	"embed"
	"io/fs"
	"path"
)

//go:embed files
var fsys embed.FS

const root = "files"

// Read returns the bytes of an embedded sticker image by filename (e.g.
// "applause.gif"). A name that isn't an embedded file returns ok=false; the
// name is joined under a fixed root and rejected if it escapes, so a caller can
// pass a catalog File value straight through without path-traversal risk.
func Read(name string) ([]byte, bool) {
	clean := path.Clean("/" + name)
	if clean == "/" {
		return nil, false
	}
	data, err := fsys.ReadFile(root + clean)
	if err != nil {
		return nil, false
	}
	return data, true
}

// Names lists every embedded image filename. Used by the catalog<->assets
// consistency test.
func Names() []string {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

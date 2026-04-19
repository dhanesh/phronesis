//go:build prod

package webfs

import (
	"embed"
	"io/fs"
)

// Satisfies: RT-9 (production build embeds the real frontend), T3, T4.
//
// `dist/` is NOT checked into git. `make build` copies the contents of
// `frontend/dist/` into `internal/webfs/dist/` immediately before invoking
// `go build -tags=prod ./cmd/phronesis`. If the directory is missing or
// empty, this compile unit fails at build time with
//   "pattern dist/*: no matching files found"
// — which is exactly the loud failure RT-9's acceptance criteria demand,
// and which prevents accidentally shipping a binary with the dev stub
// frontend.
//
//go:embed dist
var embedded embed.FS

// FS returns the embedded production frontend rooted at the `dist`
// directory so callers can read `index.html` at the top level.
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic("webfs: dist subdirectory missing from embed: " + err.Error())
	}
	return sub
}

// IsStub reports whether the current build is serving the dev stub
// frontend. The production build always returns false.
func IsStub() bool { return false }

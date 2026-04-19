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
// `go build -tags=prod ./cmd/phronesis`. If the directory is missing the
// compile fails with
//
//	"pattern dist: no matching files found"
//
// and if the directory is present-but-empty the compile fails with
//
//	"pattern dist: cannot embed directory dist: contains no embeddable files"
//
// — either failure is the loud compile-time signal RT-9's acceptance
// criteria require, preventing an accidental ship of a binary with the
// dev stub frontend.
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

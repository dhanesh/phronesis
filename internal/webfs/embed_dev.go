//go:build !prod

package webfs

import (
	"embed"
	"io/fs"
)

// Satisfies: RT-9 (dev build serves a stub so `go test ./...` doesn't
// require a prior `npm run build`), T5.
//
// The stub/ directory IS checked into git — it needs to be present at
// compile time for the //go:embed directive to succeed. Its content is a
// deliberately minimal placeholder so any developer who forgets the
// `-tags=prod` flag on a release build notices immediately.
//
// The "loud startup warning" referenced in RT-9's validation criteria
// lives in internal/app/server.go NewServer — it's emitted from the
// canonical call site rather than on first FS() use. This gives us a
// testable, once-per-startup signal without needing sync.Once state
// that tests can't reset. (Review response I5.)

//go:embed stub
var embedded embed.FS

// FS returns the embedded dev-stub frontend.
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "stub")
	if err != nil {
		panic("webfs: stub subdirectory missing from embed: " + err.Error())
	}
	return sub
}

// IsStub reports whether the current build is serving the dev stub
// frontend. The dev build always returns true.
func IsStub() bool { return true }

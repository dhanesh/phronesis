//go:build !prod

package webfs

import (
	"embed"
	"io/fs"
	"log"
	"sync"
)

// Satisfies: RT-9 (dev build serves a stub so `go test ./...` doesn't
// require a prior `npm run build`), T5.
//
// The stub/ directory IS checked into git — it needs to be present at
// compile time for the //go:embed directive to succeed. Its content is a
// deliberately minimal placeholder so any developer who forgets the
// `-tags=prod` flag on a release build notices immediately.

//go:embed stub
var embedded embed.FS

var warnOnce sync.Once

// FS returns the embedded dev-stub frontend. On first call it emits a
// one-line warning to stderr so binaries built without `-tags=prod`
// announce themselves loudly at startup. Tests call FS frequently; the
// sync.Once ensures the warning fires at most once per process.
//
// Satisfies: RT-9 validation criterion ("phronesis built without
// -tags=prod serves the stub and logs a loud startup warning").
func FS() fs.FS {
	warnOnce.Do(func() {
		log.Println("[phronesis] WARNING: dev-stub frontend active. " +
			"Build with `make build` or `go build -tags=prod ./cmd/phronesis` for production.")
	})
	sub, err := fs.Sub(embedded, "stub")
	if err != nil {
		panic("webfs: stub subdirectory missing from embed: " + err.Error())
	}
	return sub
}

// IsStub reports whether the current build is serving the dev stub
// frontend. The dev build always returns true.
func IsStub() bool { return true }

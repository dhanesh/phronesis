package snapshot

import "os"

// readDir is a thin indirection over os.ReadDir to keep the test helper
// simple. Exists so the test helper in target_test.go can use a single
// call site that matches the walker shape.
func readDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

//go:build linux && test_scratch_spine

package substrate

import "io"

// A Linux test build opens a test database on any filesystem.
// Production builds omit this tag and keep the guard.
func platformMountinfo() (io.ReadCloser, error) { return nil, nil }

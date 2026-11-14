package verbs

import (
	"fmt"
	"os"
	"testing"

	"mc/testenv"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mc-verbs-test")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := testenv.Isolate(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

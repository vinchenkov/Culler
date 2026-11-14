//go:build !darwin && !linux && !test_fake_routing

package main

import (
	"fmt"
	"os"
)

func hiddenInputIsTerminal(*os.File) bool {
	return false
}

func readHiddenTerminalLine(*os.File) ([]byte, error) {
	return nil, fmt.Errorf("hidden terminal input is unsupported on this platform")
}

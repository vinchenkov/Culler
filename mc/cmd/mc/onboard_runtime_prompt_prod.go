//go:build !test_fake_routing

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"mc/verbs"
)

var productionRuntimeAuthInteractive = runtimeAuthInputIsTerminal
var productionLiveSpendPrompt = promptLiveSpend

func runtimeAuthInputIsTerminal(stdin io.Reader) bool {
	file, ok := stdin.(*os.File)
	return ok && hiddenInputIsTerminal(file)
}

func promptLiveSpend(binding string, stdin io.Reader, stderr io.Writer) (bool, error) {
	if _, err := fmt.Fprintf(stderr,
		"Run the live acceptance for binding %s? It sends a maximum of two provider requests. [yes/no]: ", binding); err != nil {
		return false, verbs.Domainf("write live spend prompt for binding %s: %v", binding, err)
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, verbs.Domainf("read live spend answer for binding %s: %v", binding, err)
	}
	return strings.EqualFold(strings.TrimSpace(line), "yes"), nil
}

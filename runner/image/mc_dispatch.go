// The public command enters the protected executable in the runtime volume.
// The executable reads the fixed identity at /mc/run.json.
package main

import (
	"os"
	"syscall"
)

func main() {
	target := "/opt/culler-runtime/libexec/mc-real"
	args := append([]string{target}, os.Args[1:]...)
	if err := syscall.Exec(target, args, os.Environ()); err != nil {
		_, _ = os.Stderr.WriteString("mc: exec wrapper: " + err.Error() + "\n")
		os.Exit(127)
	}
}

//go:build linux

package domain

import (
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// ReadVMClock reads the monotonic clock, which excludes the time that the host sleeps.
func ReadVMClock() (VMClock, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return VMClock{}, err
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return VMClock{}, err
	}
	return VMClock{BootID: strings.TrimSpace(string(boot)), Uptime: time.Duration(ts.Nano())}, nil
}

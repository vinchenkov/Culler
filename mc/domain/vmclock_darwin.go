//go:build darwin

package domain

import (
	"time"

	"golang.org/x/sys/unix"
)

// ReadVMClock reads the uptime clock, which excludes the time that the host sleeps.
func ReadVMClock() (VMClock, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_UPTIME_RAW, &ts); err != nil {
		return VMClock{}, err
	}
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return VMClock{}, err
	}
	return VMClock{BootID: boot, Uptime: time.Duration(ts.Nano())}, nil
}

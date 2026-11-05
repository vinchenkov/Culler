package domain

import "time"

// VMClock is the awake time of the VM that runs the spine. The uptime clock
// stops while the host sleeps and does not jump at wake. A new boot identifier
// shows that the VM restarted, which stops every run container.
type VMClock struct {
	BootID string
	Uptime time.Duration
}

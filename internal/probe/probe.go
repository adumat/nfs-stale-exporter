// Package probe checks mount and server liveness with bounded waits.
package probe

import (
	"errors"
	"net"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// Leaked counts goroutines still blocked in statfs after their deadline.
// Go cannot cancel a syscall, so a timeout returns while the goroutine stays
// parked until the kernel gives up. Soft mounts bound this; hard mounts do not.
var Leaked atomic.Int64

type Result struct {
	OK       bool
	Err      error
	Duration time.Duration
	TimedOut bool
	// NotFound separates "the path is gone, or was never visible in this mount
	// namespace" from a genuinely stale handle. A pod torn down between reading
	// the mount table and probing it lands here, and so does any host mount that
	// is not propagated into the container. Neither is an application fault, so
	// callers must not report these as stale.
	NotFound bool
}

func Statfs(path string, timeout time.Duration) Result {
	start := time.Now()
	ch := make(chan error, 1)
	go func() {
		var st unix.Statfs_t
		ch <- unix.Statfs(path, &st)
	}()
	select {
	case err := <-ch:
		return Result{
			OK:       err == nil,
			Err:      err,
			Duration: time.Since(start),
			NotFound: errors.Is(err, unix.ENOENT),
		}
	case <-time.After(timeout):
		Leaked.Add(1)
		go func() { <-ch; Leaked.Add(-1) }()
		// Measured, not the deadline: a reported duration equal to the timeout
		// would be indistinguishable from a genuine slow-but-successful check.
		return Result{Duration: time.Since(start), TimedOut: true}
	}
}

func TCP(addr string, timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

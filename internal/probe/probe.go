// Package probe checks mount and server liveness with bounded waits.
package probe

import (
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
		return Result{OK: err == nil, Err: err, Duration: time.Since(start)}
	case <-time.After(timeout):
		Leaked.Add(1)
		go func() { <-ch; Leaked.Add(-1) }()
		return Result{Duration: timeout, TimedOut: true}
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

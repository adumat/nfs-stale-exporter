// Package probe checks mount and server liveness with bounded waits.
package probe

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// Leaked counts goroutines still blocked in the probe syscall past their
// deadline. Go cannot cancel a syscall, so a timeout returns while the
// goroutine stays parked until the kernel gives up. Soft mounts bound this;
// hard mounts do not.
var Leaked atomic.Int64

// inflight holds paths whose probe has not returned yet. Without this guard a
// permanently blocked mount is re-probed every cycle, and because each probe
// pins an OS thread the exporter grows unboundedly until it is OOMKilled -
// while the mount it exists to report on is still broken. With it, a hung mount
// costs at most one blocked syscall plus one reaper, no matter how long it
// stays hung.
var inflight sync.Map

type Result struct {
	OK       bool
	Err      error
	Duration time.Duration
	TimedOut bool
	// Blocked means a previous probe of this path never returned, so this cycle
	// was skipped rather than starting another one.
	Blocked bool
	// NotFound separates "the path is gone, or was never visible in this mount
	// namespace" from a genuinely stale handle. A pod torn down between reading
	// the mount table and probing it lands here, and so does any host mount that
	// is not propagated into the container. Neither is an application fault, so
	// callers must not report these as stale.
	NotFound bool
}

// Stat probes one mount by resolving its root with lstat(2), bounded by
// timeout.
//
// ⚠️ The syscall choice is the whole point of this function; do not "optimise"
// it back to statfs(2).
//
// This exporter shipped using statfs and was BLIND to the exact failure it
// exists to detect. statfs reports filesystem-level information and the kernel
// can answer it from cached superblock data WITHOUT resolving a file handle, so
// on an NFS mount whose handle has gone stale it returns success. Measured on a
// real ESTALE mount, same path, same instant:
//
//	lstat(path)   -> ESTALE ("stale file handle")
//	statfs(path)  -> success
//
// A stale handle is only observable by a syscall that actually resolves one.
//
// It must be the mount ROOT, not a path inside it: ESTALE is per-handle, and
// the root can be stale while child handles still resolve, so probing a
// subdirectory passes on a broken mount.
//
// "Root" means the root of EVERY mount in the table, which includes kubelet's
// /volume-subpaths/ binds. Those are separate NFS mounts, not subdirectories,
// and they go stale independently - do not skip them on the grounds that the
// volume root is already covered. That reasoning shipped once and cost five
// days of a silently dead mount; see the note on subPathRe in internal/mounts.
//
// Beware synthetic tests here. Deleting the export server-side makes statfs
// fail too, so a rehearsal built that way passes with either syscall and proves
// nothing. Verify against a genuinely stale handle.
func Stat(path string, timeout time.Duration) Result {
	if _, busy := inflight.Load(path); busy {
		return Result{TimedOut: true, Blocked: true}
	}
	start := time.Now()
	inflight.Store(path, struct{}{})
	ch := make(chan error, 1)
	go func() {
		var st unix.Stat_t
		// lstat, not stat: if the mount root is ever replaced by a symlink we
		// want to fail on it rather than silently follow the link off the mount
		// and report the wrong filesystem as healthy.
		err := unix.Lstat(path, &st)
		inflight.Delete(path)
		ch <- err
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

package probe

import (
	"net"
	"os"
	"testing"
	"time"
)

func TestStatOnHealthyPath(t *testing.T) {
	r := Stat("/tmp", time.Second)
	if !r.OK || r.TimedOut {
		t.Fatalf("want healthy, got %+v", r)
	}
}

func TestStatOnMissingPath(t *testing.T) {
	r := Stat("/definitely/not/here", time.Second)
	if r.OK || r.Err == nil {
		t.Fatalf("want failure, got %+v", r)
	}
}

func TestStatMissingPathIsNotFound(t *testing.T) {
	// A vanished path must be distinguishable from a stale handle: pod teardown
	// races here constantly and must not be reported as an application fault.
	r := Stat("/definitely/not/here", time.Second)
	if !r.NotFound {
		t.Fatalf("want NotFound, got %+v", r)
	}
}

func TestStatHealthyPathIsNotNotFound(t *testing.T) {
	if r := Stat("/tmp", time.Second); r.NotFound {
		t.Fatalf("healthy path must not be NotFound, got %+v", r)
	}
}

func TestTCPReachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !TCP(ln.Addr().String(), time.Second) {
		t.Error("want reachable")
	}
}

func TestTCPUnreachable(t *testing.T) {
	if TCP("127.0.0.1:1", 200*time.Millisecond) {
		t.Error("want unreachable")
	}
}

func TestStatSkipsPathAlreadyBlocked(t *testing.T) {
	// A mount whose previous probe never returned must not be probed again:
	// each blocked probe pins an OS thread, so re-probing every cycle grows
	// without bound until the exporter is OOMKilled.
	const p = "/blocked/path"
	inflight.Store(p, struct{}{})
	defer inflight.Delete(p)

	start := time.Now()
	r := Stat(p, 5*time.Second)
	if !r.Blocked || r.OK {
		t.Fatalf("want Blocked, got %+v", r)
	}
	if time.Since(start) > time.Second {
		t.Errorf("blocked path must return immediately, took %s", time.Since(start))
	}
}

func TestStatClearsInflightOnSuccess(t *testing.T) {
	if r := Stat("/tmp", time.Second); !r.OK {
		t.Fatalf("want healthy, got %+v", r)
	}
	if _, busy := inflight.Load("/tmp"); busy {
		t.Error("inflight must be cleared after a completed probe")
	}
}

// TestStatUsesLstatNotStatfsOrStat pins the syscall, because getting it wrong
// is silent: the exporter shipped on statfs and reported real stale mounts as
// healthy for three days. No unit test can manufacture an ESTALE handle, so
// this guards the choice indirectly.
//
// A dangling symlink separates all three candidates:
//
//	lstat  -> success (it stats the link itself)
//	stat   -> ENOENT  (it follows the link to nothing)
//	statfs -> ENOENT  (likewise)
//
// So OK==true here means, and only means, that the probe still calls lstat.
// If this fails, someone changed the syscall - read the comment on Stat before
// "fixing" the test.
func TestStatUsesLstatNotStatfsOrStat(t *testing.T) {
	dir := t.TempDir()
	link := dir + "/dangling"
	if err := os.Symlink(dir+"/no-such-target", link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	r := Stat(link, time.Second)
	if !r.OK {
		t.Fatalf("probe no longer uses lstat: got err=%v (stat/statfs would fail here, lstat must not)", r.Err)
	}
	if r.NotFound {
		t.Fatalf("dangling symlink reported NotFound; probe is following the link")
	}
}

package probe

import (
	"net"
	"testing"
	"time"
)

func TestStatfsOnHealthyPath(t *testing.T) {
	r := Statfs("/tmp", time.Second)
	if !r.OK || r.TimedOut {
		t.Fatalf("want healthy, got %+v", r)
	}
}

func TestStatfsOnMissingPath(t *testing.T) {
	r := Statfs("/definitely/not/here", time.Second)
	if r.OK || r.Err == nil {
		t.Fatalf("want failure, got %+v", r)
	}
}

func TestStatfsMissingPathIsNotFound(t *testing.T) {
	// A vanished path must be distinguishable from a stale handle: pod teardown
	// races here constantly and must not be reported as an application fault.
	r := Statfs("/definitely/not/here", time.Second)
	if !r.NotFound {
		t.Fatalf("want NotFound, got %+v", r)
	}
}

func TestStatfsHealthyPathIsNotNotFound(t *testing.T) {
	if r := Statfs("/tmp", time.Second); r.NotFound {
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

func TestStatfsSkipsPathAlreadyBlocked(t *testing.T) {
	// A mount whose previous statfs never returned must not be probed again:
	// each blocked probe pins an OS thread, so re-probing every cycle grows
	// without bound until the exporter is OOMKilled.
	const p = "/blocked/path"
	inflight.Store(p, struct{}{})
	defer inflight.Delete(p)

	start := time.Now()
	r := Statfs(p, 5*time.Second)
	if !r.Blocked || r.OK {
		t.Fatalf("want Blocked, got %+v", r)
	}
	if time.Since(start) > time.Second {
		t.Errorf("blocked path must return immediately, took %s", time.Since(start))
	}
}

func TestStatfsClearsInflightOnSuccess(t *testing.T) {
	if r := Statfs("/tmp", time.Second); !r.OK {
		t.Fatalf("want healthy, got %+v", r)
	}
	if _, busy := inflight.Load("/tmp"); busy {
		t.Error("inflight must be cleared after a completed probe")
	}
}

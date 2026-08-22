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

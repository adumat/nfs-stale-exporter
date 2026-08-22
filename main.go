package main

import (
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/adumat/nfs-stale-exporter/internal/mounts"
	"github.com/adumat/nfs-stale-exporter/internal/probe"
)

var version, revision = "dev", "none"

var (
	staleG = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nfs_mount_stale",
		Help: "1 if statfs on the NFS mount root failed or timed out.",
	}, []string{"mountpoint", "server", "export", "pod_uid", "volume"})

	errG = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nfs_mount_check_error",
		Help: "1 with the failure reason, absent when the check succeeded.",
	}, []string{"mountpoint", "reason"})

	durG = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nfs_mount_check_duration_seconds",
		Help: "Duration of the last statfs.",
	}, []string{"mountpoint"})

	discoveredG = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nfs_mounts_discovered",
		Help: "NFS mounts successfully probed on this node. Zero means the exporter is blind, not healthy.",
	})

	unreachableG = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nfs_mounts_unreachable",
		Help: "Mounts listed in the host mount table but not visible in this namespace (ENOENT). Not counted as stale.",
	})

	leakedG = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nfs_probe_leaked",
		Help: "Probe goroutines still blocked in statfs past their deadline.",
	})

	reachG = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nfs_server_reachable",
		Help: "1 if the NFS server's port 2049 accepted a TCP connection.",
	}, []string{"server"})

	buildG = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nfs_stale_exporter_build_info",
		Help: "Build metadata.",
	}, []string{"version", "revision"})
)

func main() {
	addr := flag.String("web.listen-address", ":9855", "metrics listen address")
	procfsPath := flag.String("path.procfs", "/proc", "procfs mountpoint")
	interval := flag.Duration("check-interval", 30*time.Second, "time between check cycles")
	mountTimeout := flag.Duration("mount-timeout", 5*time.Second, "per-mount statfs deadline")
	include := flag.String("mountpoint-include", "", "regex; empty means all NFS mounts")
	exclude := flag.String("mountpoint-exclude", "volume-subpaths", "regex of mountpoints to skip")
	maxProbes := flag.Int("max-concurrent-probes", 32, "cap on concurrent in-flight statfs calls")
	serverProbe := flag.Bool("server-probe", true, "TCP-probe each server's port 2049")
	serverTimeout := flag.Duration("server-probe-timeout", 3*time.Second, "server probe deadline")
	flag.Parse()

	incRe, err := compileOrNil(*include)
	if err != nil {
		fatal("invalid --mountpoint-include", err)
	}
	excRe, err := compileOrNil(*exclude)
	if err != nil {
		fatal("invalid --mountpoint-exclude", err)
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(staleG, errG, durG, discoveredG, unreachableG, leakedG, reachG, buildG)
	buildG.WithLabelValues(version, revision).Set(1)

	mf := hostMountsFile(*procfsPath)
	slog.Info("reading mount table", "path", mf)

	go func() {
		for {
			collect(mf, incRe, excRe, *mountTimeout, *maxProbes, *serverProbe, *serverTimeout)
			time.Sleep(*interval)
		}
	}()

	http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	slog.Info("listening", "addr", *addr, "version", version)
	srv := &http.Server{Addr: *addr, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fatal("server failed", err)
	}
}

func fatal(msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(1)
}

// compileOrNil treats an empty pattern as "no filter". A bad pattern is a
// startup error, never a panic with a goroutine dump in the pod log.
func compileOrNil(pat string) (*regexp.Regexp, error) {
	if pat == "" {
		return nil, nil
	}
	return regexp.Compile(pat)
}

// hostMountsFile returns the node's real mount table.
//
// /proc/mounts is a symlink to self/mounts, so inside a container it yields the
// CONTAINER's mount table even when the host /proc is bind-mounted in. The host
// table has to be read from PID 1 explicitly. Falls back to <procfs>/mounts when
// 1/mounts cannot be opened, which keeps the exporter usable outside a container.
func hostMountsFile(procfs string) string {
	p := filepath.Join(procfs, "1", "mounts")
	if f, err := os.Open(p); err == nil {
		_ = f.Close()
		return p
	}
	return filepath.Join(procfs, "mounts")
}

type result struct {
	m         mounts.Mount
	stale     float64
	reason    string
	seconds   float64
	unreached bool
}

func collect(mountsFile string, incRe, excRe *regexp.Regexp, mountTimeout time.Duration, maxProbes int, doServer bool, serverTimeout time.Duration) {
	f, err := os.Open(mountsFile)
	if err != nil {
		slog.Error("cannot read mounts", "path", mountsFile, "err", err)
		return
	}
	all, err := mounts.Parse(f)
	_ = f.Close()
	if err != nil {
		slog.Error("cannot parse mounts", "err", err)
		return
	}

	var sel []mounts.Mount
	for _, m := range all {
		if incRe != nil && !incRe.MatchString(m.Mountpoint) {
			continue
		}
		if excRe != nil && excRe.MatchString(m.Mountpoint) {
			continue
		}
		sel = append(sel, m)
	}

	results := make([]result, len(sel))
	sem := make(chan struct{}, maxProbes)
	var wg sync.WaitGroup
	for i, m := range sel {
		wg.Add(1)
		go func(i int, m mounts.Mount) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			r := probe.Statfs(m.Mountpoint, mountTimeout)
			res := result{m: m, seconds: r.Duration.Seconds()}
			switch {
			case r.OK:
			case r.NotFound:
				// Vanished or never visible here. Reporting this as stale would
				// fire an alert on ordinary pod teardown.
				res.unreached = true
			default:
				res.stale = 1
				res.reason = "timeout"
				if r.Err != nil {
					res.reason = r.Err.Error()
				}
			}
			results[i] = res
		}(i, m)
	}
	wg.Wait()

	var reach map[string]float64
	if doServer {
		seen := map[string]bool{}
		for _, m := range sel {
			if m.Server != "" {
				seen[m.Server] = true
			}
		}
		reach = make(map[string]float64, len(seen))
		for s := range seen {
			if probe.TCP(net.JoinHostPort(s, "2049"), serverTimeout) {
				reach[s] = 1
			} else {
				reach[s] = 0
			}
		}
	}

	// Publish only once every probe has finished. Resetting up front and filling
	// in as results arrive leaves the series ABSENT for the duration of a slow
	// check - which is exactly when a hung or unreachable mount is being
	// measured. A scrape landing in that window resets pending alerts and makes
	// gated KEDA queries evaluate empty.
	publish(results, reach, doServer)
}

func publish(results []result, reach map[string]float64, doServer bool) {
	staleG.Reset()
	errG.Reset()
	durG.Reset()

	var probed, unreachable int
	for _, r := range results {
		durG.WithLabelValues(r.m.Mountpoint).Set(r.seconds)
		if r.unreached {
			unreachable++
			continue
		}
		probed++
		staleG.WithLabelValues(r.m.Mountpoint, r.m.Server, r.m.Export, r.m.PodUID, r.m.Volume).Set(r.stale)
		if r.stale == 1 {
			errG.WithLabelValues(r.m.Mountpoint, r.reason).Set(1)
			slog.Warn("mount unhealthy", "mountpoint", r.m.Mountpoint, "reason", r.reason)
		}
	}
	discoveredG.Set(float64(probed))
	unreachableG.Set(float64(unreachable))
	leakedG.Set(float64(probe.Leaked.Load()))

	if doServer {
		reachG.Reset()
		for s, v := range reach {
			reachG.WithLabelValues(s).Set(v)
		}
	}
}

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
		Help: "NFS mounts seen on this node. Zero means the exporter is blind, not healthy.",
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
	maxProbes := flag.Int("max-concurrent-probes", 32, "cap on in-flight statfs calls")
	serverProbe := flag.Bool("server-probe", true, "TCP-probe each server's port 2049")
	serverTimeout := flag.Duration("server-probe-timeout", 3*time.Second, "server probe deadline")
	flag.Parse()

	var incRe, excRe *regexp.Regexp
	if *include != "" {
		incRe = regexp.MustCompile(*include)
	}
	if *exclude != "" {
		excRe = regexp.MustCompile(*exclude)
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(staleG, errG, durG, discoveredG, leakedG, reachG, buildG)
	buildG.WithLabelValues(version, revision).Set(1)

	go func() {
		for {
			collect(filepath.Join(*procfsPath, "mounts"), incRe, excRe, *mountTimeout, *maxProbes, *serverProbe, *serverTimeout)
			time.Sleep(*interval)
		}
	}()

	http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	slog.Info("listening", "addr", *addr, "version", version)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
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
	discoveredG.Set(float64(len(sel)))

	// Reset so mounts that disappeared stop reporting.
	staleG.Reset()
	errG.Reset()
	durG.Reset()

	sem := make(chan struct{}, maxProbes)
	var wg sync.WaitGroup
	for _, m := range sel {
		wg.Add(1)
		go func(m mounts.Mount) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			r := probe.Statfs(m.Mountpoint, mountTimeout)
			v := 0.0
			if !r.OK {
				v = 1
				reason := "timeout"
				if r.Err != nil {
					reason = r.Err.Error()
				}
				errG.WithLabelValues(m.Mountpoint, reason).Set(1)
				slog.Warn("mount unhealthy", "mountpoint", m.Mountpoint, "reason", reason)
			}
			staleG.WithLabelValues(m.Mountpoint, m.Server, m.Export, m.PodUID, m.Volume).Set(v)
			durG.WithLabelValues(m.Mountpoint).Set(r.Duration.Seconds())
		}(m)
	}
	wg.Wait()
	leakedG.Set(float64(probe.Leaked.Load()))

	if doServer {
		reachG.Reset()
		seen := map[string]bool{}
		for _, m := range sel {
			if m.Server != "" {
				seen[m.Server] = true
			}
		}
		for s := range seen {
			v := 0.0
			if probe.TCP(net.JoinHostPort(s, "2049"), serverTimeout) {
				v = 1
			}
			reachG.WithLabelValues(s).Set(v)
		}
	}
}

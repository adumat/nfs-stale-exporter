// Package mounts parses /proc/mounts and decodes kubelet NFS volume paths.
package mounts

import (
	"bufio"
	"io"
	"regexp"
	"strings"
)

// The plugin segment is deliberately wide (`kubernetes.io~[^/]+`) rather than
// pinned to `~nfs`: csi-driver-nfs mounts under `~csi/<pv>/mount`, and the pod
// UID sits in the same place whichever plugin did the mounting. Only NFS-fstype
// lines reach here anyway. Pinning it would still probe CSI mounts but export
// them with an empty pod_uid, dropping them silently from the identity join.
var kubeletRe = regexp.MustCompile(`^/var/lib/kubelet/pods/([^/]+)/volumes/kubernetes\.io~[^/]+/([^/]+)(?:/mount)?$`)

// subPath binds are SEPARATE NFS mounts, not subdirectories of the volume root,
// and they go stale independently of it.
//
// This was originally assumed to be impossible - the old comment here claimed
// subPath binds "stay resolvable while the root is already stale", and
// main.go excluded /volume-subpaths/ by default on that basis. The opposite was
// measured on 2026-09-01: metube's root mount lstat'd fine on two different
// nodes while its subPath bind returned ESTALE, so the container saw a dead
// /downloads and the exporter reported the node healthy. Because the KEDA
// scaler reads that metric, the self-heal never fired either.
//
//	.../volumes/kubernetes.io~nfs/metube-media    -> lstat OK
//	.../volume-subpaths/metube-media/app/2        -> ESTALE
//
// Decoding pod UID and volume from these is what keeps them in the identity
// join; exporting them with empty labels would drop them again, just further
// downstream. The mountpoint label still differs from the root's, so the two
// coexist as separate series for the same (pod, volume).
var subPathRe = regexp.MustCompile(`^/var/lib/kubelet/pods/([^/]+)/volume-subpaths/([^/]+)/[^/]+/\d+$`)

type Mount struct {
	Device     string
	Mountpoint string
	FSType     string
	Server     string
	Export     string
	PodUID     string
	Volume     string
}

// unescape decodes the octal escapes /proc/mounts uses for space, tab, newline, backslash.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			v, ok := 0, true
			for _, c := range s[i+1 : i+4] {
				if c < '0' || c > '7' {
					ok = false
					break
				}
				v = v*8 + int(c-'0')
			}
			if ok {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func Parse(r io.Reader) ([]Mount, error) {
	var out []Mount
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || (f[2] != "nfs" && f[2] != "nfs4") {
			continue
		}
		m := Mount{Device: unescape(f[0]), Mountpoint: unescape(f[1]), FSType: f[2]}
		if i := strings.Index(m.Device, ":"); i >= 0 {
			m.Server, m.Export = m.Device[:i], m.Device[i+1:]
		}
		if g := kubeletRe.FindStringSubmatch(m.Mountpoint); g != nil {
			m.PodUID, m.Volume = g[1], g[2]
		} else if g := subPathRe.FindStringSubmatch(m.Mountpoint); g != nil {
			m.PodUID, m.Volume = g[1], g[2]
		}
		out = append(out, m)
	}
	return out, sc.Err()
}

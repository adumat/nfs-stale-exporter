// Package mounts parses /proc/mounts and decodes kubelet NFS volume paths.
package mounts

import (
	"bufio"
	"io"
	"regexp"
	"strings"
)

// Only the volume ROOT is a valid probe target. subPath binds are children and
// stay resolvable while the root is already stale.
var kubeletRe = regexp.MustCompile(`^/var/lib/kubelet/pods/([^/]+)/volumes/kubernetes\.io~nfs/([^/]+)$`)

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
		}
		out = append(out, m)
	}
	return out, sc.Err()
}

package mounts

import "strings"
import "testing"

const fixture = `sysfs /sys sysfs rw,nosuid 0 0
elizabeth.lan:/mnt/user/media /var/lib/kubelet/pods/00bb3ecd-7dd1-46a5-928f-5615cf40f30f/volumes/kubernetes.io~nfs/media nfs4 rw,noatime,vers=4.2 0 0
elizabeth.lan:/mnt/user/cloud /var/lib/kubelet/pods/f225670d-48a2-4806-9670-71f74c10d529/volume-subpaths/paperless-media/app/1 nfs4 rw 0 0
nas:/exports/with\040space /mnt/odd nfs rw 0 0
/dev/sda1 /boot ext4 rw 0 0
elizabeth.lan:/mnt/user/data /var/lib/kubelet/pods/aaaa-bbbb-cccc/volumes/kubernetes.io~csi/pvc-1234/mount nfs4 rw 0 0
`

func TestParseSelectsOnlyNFS(t *testing.T) {
	got, err := Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 nfs mounts, got %d", len(got))
	}
}

func TestParseDecodesKubeletPath(t *testing.T) {
	got, _ := Parse(strings.NewReader(fixture))
	m := got[0]
	if m.Server != "elizabeth.lan" || m.Export != "/mnt/user/media" {
		t.Errorf("device split wrong: %+v", m)
	}
	if m.PodUID != "00bb3ecd-7dd1-46a5-928f-5615cf40f30f" || m.Volume != "media" {
		t.Errorf("kubelet decode wrong: %+v", m)
	}
}

func TestParseDecodesSubPath(t *testing.T) {
	// This asserted the OPPOSITE until 2026-09-01: subPath binds were left
	// undecoded and excluded, on the theory that they stay resolvable while the
	// root goes stale. Measured on two nodes, the root lstat'd fine while the
	// subPath bind returned ESTALE, so the container saw a dead mount and the
	// exporter called the node healthy. They are separate mounts that fail
	// separately, and they need pod identity to survive the join.
	got, _ := Parse(strings.NewReader(fixture))
	m := got[1]
	if m.PodUID != "f225670d-48a2-4806-9670-71f74c10d529" || m.Volume != "paperless-media" {
		t.Errorf("subPath decode wrong: %+v", m)
	}
}

// A subPath bind and its volume root are distinct series for the same pod and
// volume, separated only by mountpoint. If they ever collapsed into one series
// the stale one could be masked by the healthy one.
func TestSubPathAndRootShareIdentityButNotMountpoint(t *testing.T) {
	const both = `srv:/exp /var/lib/kubelet/pods/uid-1/volumes/kubernetes.io~nfs/vol-a nfs4 rw 0 0
srv:/exp/sub /var/lib/kubelet/pods/uid-1/volume-subpaths/vol-a/app/2 nfs4 rw 0 0
`
	got, _ := Parse(strings.NewReader(both))
	if len(got) != 2 {
		t.Fatalf("want 2 mounts, got %d", len(got))
	}
	if got[0].PodUID != got[1].PodUID || got[0].Volume != got[1].Volume {
		t.Errorf("identity must match: %+v vs %+v", got[0], got[1])
	}
	if got[0].Mountpoint == got[1].Mountpoint {
		t.Errorf("mountpoints must differ, else one series masks the other")
	}
}

func TestParseUnescapesOctal(t *testing.T) {
	got, _ := Parse(strings.NewReader(fixture))
	if got[2].Export != "/exports/with space" {
		t.Errorf("octal unescape failed: %q", got[2].Export)
	}
}

func TestParseDecodesCSIPath(t *testing.T) {
	// csi-driver-nfs mounts under kubernetes.io~csi/<pv>/mount. The pod UID sits
	// in the same place regardless of which plugin mounted the volume, and
	// without decoding it these mounts export metrics with an empty pod_uid and
	// then vanish from the identity join - a silent partial failure.
	got, _ := Parse(strings.NewReader(fixture))
	m := got[3]
	if m.PodUID != "aaaa-bbbb-cccc" || m.Volume != "pvc-1234" {
		t.Errorf("csi decode wrong: %+v", m)
	}
}

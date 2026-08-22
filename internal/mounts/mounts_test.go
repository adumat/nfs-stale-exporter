package mounts

import "strings"
import "testing"

const fixture = `sysfs /sys sysfs rw,nosuid 0 0
elizabeth.lan:/mnt/user/media /var/lib/kubelet/pods/00bb3ecd-7dd1-46a5-928f-5615cf40f30f/volumes/kubernetes.io~nfs/media nfs4 rw,noatime,vers=4.2 0 0
elizabeth.lan:/mnt/user/cloud /var/lib/kubelet/pods/f225670d-48a2-4806-9670-71f74c10d529/volume-subpaths/paperless-media/app/1 nfs4 rw 0 0
nas:/exports/with\040space /mnt/odd nfs rw 0 0
/dev/sda1 /boot ext4 rw 0 0
`

func TestParseSelectsOnlyNFS(t *testing.T) {
	got, err := Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 nfs mounts, got %d", len(got))
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

func TestParseLeavesSubPathUndecoded(t *testing.T) {
	got, _ := Parse(strings.NewReader(fixture))
	if got[1].PodUID != "" || got[1].Volume != "" {
		t.Errorf("volume-subpaths must not decode as a volume root: %+v", got[1])
	}
}

func TestParseUnescapesOctal(t *testing.T) {
	got, _ := Parse(strings.NewReader(fixture))
	if got[2].Export != "/exports/with space" {
		t.Errorf("octal unescape failed: %q", got[2].Export)
	}
}

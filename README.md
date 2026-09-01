# nfs-stale-exporter

A Prometheus exporter that detects stale NFS mounts on Kubernetes nodes.

Installation and configuration: [INSTALL.md](INSTALL.md)

## The problem

A pod's NFS mount can go stale (`ESTALE`) while the pod keeps reporting `Running`,
`1/1`, `Ready=True`. No kubelet event fires, and `/proc/mounts` still looks normal.
Nothing in a stock cluster detects this; recovery needs a manual `kubectl delete pod`.

Reproduced deterministically by re-exporting a share with a changed `fsid`.

## Why not node_exporter

kube-prometheus excludes `/var/lib/kubelet/.+` from the filesystem collector by policy,
and node_exporter runs unprivileged, so `statfs` returns `permission denied` for every
kubelet mount ([prometheus/node_exporter#1831](https://github.com/prometheus/node_exporter/issues/1831)).

Measured:

| runAs | result |
|---|---|
| 65534 | all mounts read `1` |
| 65534 + `CAP_DAC_READ_SEARCH` | identical |
| 0 | healthy `0`, stale `1` with `err="stale file handle"` |

## Why not systemd automount

The usual fix on Docker and bare hosts. Unavailable here because kubelet owns the mount
lifecycle.

## Probe the root of every mount

A child handle can stay valid while the mount root is already stale, so a probe that reads
a known **subdirectory** passes on a broken mount. Probe the root.

"Root" means the root of *every* mount in the table — and that includes kubelet's
`volume-subpaths` binds. A `subPath` looks like a subdirectory but kubelet implements it as
a **separate NFS mount**, which goes stale independently of the volume root:

```
elizabeth.lan:/mnt/user/media            → .../volumes/kubernetes.io~nfs/metube-media   lstat OK
elizabeth.lan:/mnt/user/media/downloads  → .../volume-subpaths/metube-media/app/2       ESTALE
```

Earlier versions excluded `volume-subpaths` by default, reasoning that the volume root
already covered it. It does not. Measured on two separate nodes on 2026-09-01: the root
probed healthy while the container saw a dead `/downloads`, and because the KEDA scaler
reads this metric, the self-heal never fired either — the mount stayed broken for five days
until it was fixed by hand.

Both mounts are now probed. They share `pod_uid` and `volume` and differ by `mountpoint`,
so they are separate series for the same pod and an aggregation like
`max by (namespace, deployment)` flags the deployment if *either* is stale.

## Probe with `lstat`, not `statfs`

A stale handle is only visible to a syscall that actually **resolves** one. `statfs()`
reports filesystem-level information and the kernel can answer it from cached superblock
data without touching the handle, so it returns success on a mount that is already stale.

Measured on a real ESTALE mount, same path, same instant:

```
lstat(path)   -> ESTALE ("stale file handle")
statfs(path)  -> success
```

**v0.1.0 and earlier used `statfs`** and therefore reported genuinely stale mounts as
healthy — the exact failure this exporter exists to detect. If you are running v0.1.0,
upgrade.

⚠️ This also makes synthetic tests misleading. Removing the export server-side makes
`statfs` fail too, so a rehearsal built that way passes with either syscall and proves
nothing. Verify against a genuinely stale handle.

## Metrics

| metric | description |
|---|---|
| `nfs_mount_stale{mountpoint,server,export,pod_uid,volume}` | `1` if the mount is stale, else `0` |
| `nfs_mount_check_error{mountpoint,reason}` | `1` if the last check errored; absent when healthy |
| `nfs_mount_check_duration_seconds{mountpoint}` | time taken to check one mount |
| `nfs_mounts_discovered` | NFS mounts probed on the node. Zero means blind, not healthy |
| `nfs_mounts_unreachable` | mounts in the host table not visible here (ENOENT); **not** counted as stale |
| `nfs_probe_leaked` | probes still blocked past their timeout; a hung mount is probed once, not once per cycle |
| `nfs_server_reachable{server}` | `1` if the NFS server responds, else `0` |
| `nfs_stale_exporter_build_info{version,revision}` | build metadata, always `1` |

## Quick start

```bash
helm install nfs-stale-exporter \
  oci://ghcr.io/adumat/charts/nfs-stale-exporter \
  -n monitoring --create-namespace
```

The chart ships the DaemonSet, PodMonitor, recording rules and alerts. Plain manifests
are in [`deploy/`](deploy/) if you would rather not use Helm.

See [INSTALL.md](INSTALL.md) for configuration, verification, and optional automatic
recovery with KEDA.

## License

Apache-2.0

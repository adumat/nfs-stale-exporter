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

## Probe the root, not a subdirectory

A child handle (e.g. a `subPath` bind) can stay valid while the mount root is already
stale, so a probe that reads a known subdirectory passes on a broken mount. This is why
`volume-subpaths` binds are excluded — only the volume root is probed.

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

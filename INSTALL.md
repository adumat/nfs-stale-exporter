# Installing nfs-stale-exporter

## Requirements

- Linux nodes; NFS volumes mounted by kubelet (inline `type: nfs` or NFS-backed PVs).
- Prometheus (this guide assumes prometheus-operator CRDs).
- Ability to run one DaemonSet as uid 0. See [Why root](#why-root).
- A namespace **not** enforcing the `baseline` Pod Security Standard. `hostPath` volumes
  and added capabilities are forbidden at baseline, so pods are rejected outright; the
  namespace needs `pod-security.kubernetes.io/enforce: privileged`.

## 1. Install

```bash
kubectl apply -n monitoring -f deploy/daemonset.yaml
kubectl apply -n monitoring -f deploy/podmonitor.yaml
```

Pin a release rather than `latest`:

```yaml
image: ghcr.io/adumat/nfs-stale-exporter:v0.1.0
```

Check [the package page](https://github.com/adumat/nfs-stale-exporter/pkgs/container/nfs-stale-exporter)
for available tags — GHCR packages are private until their visibility is made public, so a
pull failure here is a permissions problem, not a missing image.

### Why root

`statfs()` on `/var/lib/kubelet/pods/<uid>/volumes/...` requires traversing
root-owned directories. Measured on a live cluster:

| runAs | result |
|---|---|
| 65534 | **every** mount reports stale, `reason="permission denied"` |
| 65534 + `CAP_DAC_READ_SEARCH` | identical — Kubernetes sets no ambient capabilities |
| 0 | healthy mounts `0`, stale mount `1` with `reason="stale file handle"` |

An unprivileged install produces a metric that is permanently 1 and therefore useless.
Everything else is dropped: `drop: [ALL]` bar `DAC_READ_SEARCH`, no privilege escalation,
read-only rootfs, read-only hostPaths, **no ServiceAccount token and no RBAC**.

### Mount propagation is mandatory

```yaml
- name: kubelet
  mountPath: /var/lib/kubelet     # same path as the host, so statfs resolves /proc/mounts paths
  readOnly: true
  mountPropagation: HostToContainer
```

Without `HostToContainer` the exporter only sees mounts that existed when it started.
Every pod scheduled later is invisible, and the exporter reports healthy.

## 2. Configuration

| flag | default | when to change |
|---|---|---|
| `--web.listen-address` | `:9855` | port conflict |
| `--path.procfs` | `/proc` | set `/host/proc` in-cluster |
| `--check-interval` | `30s` | |
| `--mount-timeout` | `5s` | raise on slow servers; a timeout counts as stale |
| `--mountpoint-include` | *(all NFS)* | restrict to one server or path |
| `--mountpoint-exclude` | `volume-subpaths` | leave alone; see below |
| `--max-concurrent-probes` | `32` | lower on nodes with very many mounts |
| `--server-probe` | `true` | disable if you already probe port 2049 |
| `--server-probe-timeout` | `3s` | |

**Do not remove the `volume-subpaths` exclusion.** A `subPath` bind is a *child* of the
volume. A child handle can stay valid while the mount root is already stale, so probing
one reports healthy on a broken mount. Only the volume root is a valid target.

## 3. Verify

The image is `scratch` — no shell, no `wget`, so `kubectl exec` cannot work. Port-forward
a single pod instead:

```bash
POD=$(kubectl get pod -n monitoring -l app.kubernetes.io/name=nfs-stale-exporter \
        -o jsonpath='{.items[0].metadata.name}')
kubectl port-forward -n monitoring "$POD" 9855:9855 &
curl -s localhost:9855/metrics | grep -E '^nfs_(mounts_discovered|mounts_unreachable|probe_leaked)'
kill %1
```

Three checks that matter:

1. `nfs_mounts_discovered` equals the NFS mount count on that node
   (`grep -c 'kubernetes.io~nfs' /proc/mounts`). **Zero means blind, not healthy.**
2. Every healthy mount reads `nfs_mount_stale 0`. If they all read `1` with
   `reason="permission denied"`, the securityContext did not apply.
3. Schedule a new NFS pod *after* the exporter is running and confirm it appears —
   this is the propagation check.

## 4. Map mounts to applications

The exporter emits `pod_uid` and never talks to the Kubernetes API. Join to identity in
Prometheus using kube-state-metrics. Expose the pod label first:

```yaml
# kube-state-metrics values
metricLabelsAllowlist:
  - pods=[app.kubernetes.io/name]
```

```yaml
- record: nfs:mount_stale:app
  expr: |-
    max by (namespace, app) (
      label_replace(
          label_replace(nfs_mount_stale, "uid", "$1", "pod_uid", "(.+)")
        * on (uid) group_left (namespace, pod) kube_pod_info
        * on (namespace, pod) group_left (label_app_kubernetes_io_name) kube_pod_labels,
        "app", "$1", "label_app_kubernetes_io_name", "(.+)")
    )
```

> kube-state-metrics exposes pod labels as `label_<sanitised name>`, so
> `app.kubernetes.io/name` arrives as `label_app_kubernetes_io_name` — never as `app`.
> Joining on `app` directly silently produces `app=""` for everything, and a KEDA query
> filtering `app="myapp"` then matches nothing and never scales the app down. Substitute
> your own label in **both** places if you use a different one, and confirm the rule
> returns one series per app before relying on it.

## 5. Alert

```yaml
- alert: NfsMountStale
  expr: nfs:mount_stale:app == 1
  for: 2m
  labels: {severity: warning}
  annotations:
    summary: "{{ $labels.app }} has a stale NFS mount"

- alert: NfsMountStaleUnrecovered
  expr: nfs:mount_stale:app == 1
  for: 20m
  labels: {severity: critical}
  annotations:
    summary: "{{ $labels.app }} stale NFS mount was not recovered automatically"

- alert: NfsExporterSeesNoMounts
  expr: nfs_mounts_discovered == 0
  for: 15m
  labels: {severity: warning}
  annotations:
    summary: "nfs-stale-exporter sees no mounts on {{ $labels.node }}"
```

Two tiers on purpose: the first is expected to self-resolve once recovery runs; the second
fires only when it did not, and is the one worth paging on.

## 6. Automatic recovery (optional)

The fix for a stale mount is to recreate the pod — kubelet unmounts on teardown and mounts
cleanly. With [KEDA](https://keda.sh), scaling to zero *is* that recreation, so no custom
controller and no delete permissions are needed.

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: myapp
spec:
  advanced: {restoreToOriginalReplicaCount: true}
  pollingInterval: 30
  cooldownPeriod: 60
  minReplicaCount: 0
  maxReplicaCount: 1
  scaleTargetRef: {name: myapp}
  triggers:
    - type: prometheus
      metadata:
        serverAddress: http://prometheus-operated.monitoring.svc:9090
        threshold: "1"
        ignoreNullValues: "0"
        query: |-
          1 - (max(min_over_time(nfs:mount_stale:app{app="myapp"}[5m])) or vector(0))
```

`min_over_time(...[5m])` is the grace period — the mount must be stale for the whole
window before the pod is bounced, so the alert reaches you first.

> **`or vector(0)` is not optional.** Scaling to zero removes the pod, its mount, and
> therefore the series — so the query must still return a value with no series present.
> `or vector(0)` supplies it. Without that, `ignoreNullValues: "0"` makes KEDA treat the
> empty result as a scaler **error** (it is `true` by default, where an empty result would
> instead read as `0`); either way the app does not come back cleanly.
> Verify by evaluating the query against an app that is already scaled to zero: it must
> return `1`.

If the NFS server itself is down, bouncing pods achieves nothing. Gate on reachability so
workloads park instead of thrashing:

```
(max(nfs_server_reachable{server="nas.example.com"}) or vector(0))
  * on() (1 - (max(min_over_time(nfs:mount_stale:app{app="myapp"}[5m])) or vector(0)))
```

Scope the server label: an unscoped `min()` parks **every** gated app whenever any one NFS
server is unreachable. The `or vector(0)` is needed here too — with `--server-probe=false`,
or while the DaemonSet is down, the series is absent and the whole expression would
otherwise evaluate empty.

## Troubleshooting

| symptom | cause |
|---|---|
| every mount reads `1`, `reason="permission denied"` | not running as uid 0 |
| `nfs_mounts_discovered` is 0 but mounts exist | missing `mountPropagation: HostToContainer`, or wrong `--path.procfs` |
| `nfs_mounts_unreachable` is high | the node has NFS mounts outside the propagated hostPath. Mount them in, or narrow `--mountpoint-include` |
| new pods never appear | same as above |
| `nfs_probe_leaked` climbing | mounts hung, not stale. Go cannot cancel a blocked syscall; use `soft` mount options so the kernel gives up |
| a stale app never scales back up | missing `or vector(0)` in the KEDA query |

# Changelog

## 0.1.0 (2026-08-22)


### Features

* collector loop and metrics endpoint ([aa7d330](https://github.com/adumat/nfs-stale-exporter/commit/aa7d330cc8c1cfef282be022530a6b3b12fa74c4))
* decode csi-driver-nfs paths, skip re-probing blocked mounts ([1dbe19e](https://github.com/adumat/nfs-stale-exporter/commit/1dbe19e1468c71585b9b50e537143489fd8fc3d1))
* helm chart rendering daemonset, podmonitor and prometheus rules ([6cfb685](https://github.com/adumat/nfs-stale-exporter/commit/6cfb6859c254a3d6cabd897e2a40e857a83b9d3c))
* parse /proc/mounts and decode kubelet NFS paths ([405aa9e](https://github.com/adumat/nfs-stale-exporter/commit/405aa9e87569405435d5edf6c0d1e8419313ee8e))
* statfs and tcp probes with bounded waits ([f3b6096](https://github.com/adumat/nfs-stale-exporter/commit/f3b60962980c9dd92f6bc33595fd450a0f3ea981))


### Bug Fixes

* publish metrics atomically, read host mount table, separate ENOENT from stale ([4ff5b27](https://github.com/adumat/nfs-stale-exporter/commit/4ff5b271afa76f6834cb12768b7f4168a212bce2))
* reachability gate must fail open, not park apps permanently ([ab29f84](https://github.com/adumat/nfs-stale-exporter/commit/ab29f84f094b8be0e2c40eb4b49eeb1c3e2e72a7))

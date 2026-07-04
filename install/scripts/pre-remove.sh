#!/bin/sh
set -e

# Only stop and disable the service on a real removal, not during an upgrade.
# Otherwise the old package's cleanup would disable the service that the new
# version just enabled, leaving it disabled after the upgrade.
# rpm passes the number of remaining installs ("0" on removal); deb passes an
# action word ("remove"/"purge" on removal, "upgrade" while upgrading).
case "$1" in
  0 | remove | purge)
    systemctl stop nvidia_gpu_exporter.service || true
    systemctl disable nvidia_gpu_exporter.service || true
    systemctl daemon-reload
    ;;
esac

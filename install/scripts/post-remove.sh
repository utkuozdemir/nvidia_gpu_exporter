#!/bin/sh
set -e

# Only remove the user on a real removal, not during an upgrade. Otherwise an
# "rpm -U" upgrade, which cleans up the old package after installing the new
# one, would delete the user the new version still needs.
# rpm passes the number of remaining installs ("0" on removal); deb passes an
# action word ("remove"/"purge" on removal, "upgrade" while upgrading).
case "$1" in
  0 | remove | purge)
    userdel -f nvidia_gpu_exporter || true
    systemctl daemon-reload
    ;;
esac

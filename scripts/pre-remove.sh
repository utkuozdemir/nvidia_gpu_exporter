#!/bin/sh
set -e

systemctl stop nvidia_gpu_exporter.service || true
systemctl disable nvidia_gpu_exporter.service || true

systemctl daemon-reload

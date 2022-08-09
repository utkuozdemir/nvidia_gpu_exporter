#!/bin/sh
set -e

userdel -f nvidia_gpu_exporter || true

systemctl daemon-reload

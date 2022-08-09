#!/bin/sh
set -e

PID1=$(ps --no-headers -o comm 1)

if [ "$PID1" != systemd ]; then
	echo "Only systemd is supported but detected pid 1: $PID1"
	exit 1
fi

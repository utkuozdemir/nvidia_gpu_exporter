#!/bin/sh
# Simulates a wedged nvidia-smi for tests: spawns a child that inherits and
# holds the stdout pipe long past any test timeout. Killing this shell leaves
# the child alive, so waiting on the pipe alone would block until the child
# exits; only the wait delay on the command unblocks it.
sleep 30 &
wait

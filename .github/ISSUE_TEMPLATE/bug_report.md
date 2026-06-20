---
name: Bug report
about: Create a report to help us improve
title: ''
labels: ''
assignees: ''

---

## Describe the bug

A clear and concise description of what the bug is.

## To reproduce

Steps to reproduce the behavior:

1. Run command '...'
2. See error

## Expected behavior

What you expected to happen.

## Console output / logs

Any error output or logs that help us diagnose the problem.

## GPU diagnostics

The biggest help for a GPU bug is your actual `nvidia-smi` output. This collector
grabs it and prints one file to attach here:

```bash
curl -fsSLO https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/master/testdata/captures/collect.sh
bash collect.sh        # add --load to also capture an under-load sample
```

It needs only `bash` and `nvidia-smi`, runs read-only commands (it changes
nothing), and masks identifiers like GPU UUID, serial and hostname by default. It
works on Linux, WSL2, and Git-Bash on Windows, writes a single `.txt`, and prints
its path. Drag that file into this issue.

## Environment

- nvidia_gpu_exporter version + architecture [e.g. `v1.3.2 - linux_x86_64`]
- Installation method [e.g. binary download, Docker, homebrew, scoop]
- GPU model [e.g. `GeForce RTX 2080 Super`]

## Additional context

Anything else that might help.

# GPU output captures

This directory holds raw `nvidia-smi` output captured from real machines. The
exporter works by running `nvidia-smi` and parsing its text/CSV output, and that
output varies a lot:

- between GPU models (consumer vs datacenter, MIG, NVLink, ECC, ...),
- between driver versions (fields get renamed, added, deprecated),
- between operating systems and architectures (Linux, Windows/WDDM, WSL2, ...),
- and between idle and under load (utilization/power/clocks/processes).

Checking real samples in lets us develop and test the exporter offline, and
reproduce parsing bugs on hardware we don't physically own.

> Got a GPU we don't have yet? Please contribute a capture. It's one command.
> See [How to contribute](#how-to-contribute-i-have-a-cool-gpu).

## One capture = one file

Each capture is a single self-contained `.txt` file, named by its keys
(`<os>-<arch>__<model>__<driver>`):

```text
testdata/captures/<os>-<arch>__<model>__<driver>.txt
e.g. testdata/captures/linux-x86_64__nvidia-geforce-rtx-2080-super__595.71.05.txt
```

That one file is the whole unit: you attach it to a bug report, and you commit it
in a pull request. The same file in both places.

Inside, it's a short metadata header followed by one clearly separated section
per command. The header is deliberately minimal: the GPU model and driver are in
the filename, and every other value nvidia-smi reports lives in the raw sections
below. The header only records what nvidia-smi does not tell you (collection
time, whether it was masked, the host OS, and the synthetic load), so there is no
re-parsing of nvidia-smi output that could drift when a driver renames a field.

```text
################################################################################
# nvidia_gpu_exporter GPU capture
################################################################################
collected_at:   ...
masked:         yes
os / kernel / arch / load


################################################################################
# capabilities :: help-query-gpu
# $ nvidia-smi --help-query-gpu
################################################################################
<output>


################################################################################
# idle :: query-gpu (csv, what the exporter parses)
# $ nvidia-smi --query-gpu=... --format=csv
################################################################################
<output>
...
```

Captured sections: `nvidia-smi --version` and `-h`, the `--help-query-*` field
lists, `-L`, `topo -m`, `nvlink -s`, and the derived query-gpu field list. Then,
for each of the idle and load states: the default table, `-q`, the full
`--query-gpu` CSV (exactly what the exporter parses), `--query-compute-apps`,
`--query-accounted-apps`, `--query-retired-pages`, and `pmon` / `dmon`.

A failing command (a feature your card doesn't support) is kept too. That it
prints `[N/A]` or an error is itself useful data.

## Privacy / masking

By default the collector masks identifiers that could fingerprint a machine,
using format-preserving placeholders (tests only care about the shape of the
data, not the real value):

- GPU UUID → `GPU-00000000-0000-0000-0000-000000000000`
- Serial → `0000000000000`
- Hostname → `redacted-host`

Left as-is, since they are not sensitive: PCI bus id, VBIOS version, model name,
memory size, compute capability. Process names are kept, because the per-process
metrics feature needs them, so please skim the `query-compute-apps` and `pmon`
sections and redact anything proprietary. `--no-mask` turns masking off, which is
not recommended for anything you publish.

## How to contribute (I have a cool GPU)

Requirements are tiny: `nvidia-smi`, `bash`, and the standard core utilities it
uses (`awk`, `sed`, `tr`, `paste`, `head`). That covers Linux, WSL2, and Git-Bash
on Windows. `ffmpeg` is optional, for the under-load capture.

```bash
# from a clone of the repo:
./testdata/captures/collect.sh          # idle only
./testdata/captures/collect.sh --load   # also capture an under-load sample (needs ffmpeg)
```

It runs read-only commands (it changes nothing), masks identifiers by default,
and prints the path of the single `.txt` it wrote. Then commit that file and open
a PR, or if you'd rather not use git, attach the file to a GitHub issue or email
it.

You can also run it outside a clone (for example downloaded on its own). It just
writes the file next to itself and prints the path.

### On Windows

The script just needs a `bash` with the standard core utilities (`awk`, `sed`,
`tr`, `paste`, `head`) and `nvidia-smi` on `PATH`. We recommend **Git Bash** (it
ships with [Git for Windows](https://git-scm.com/download/win)) because it has all
of that out of the box and still calls the native `nvidia-smi.exe`. MSYS2, Cygwin
or WSL2 work too; plain PowerShell or CMD do not, since they have no `bash`. Open
your shell, `cd` into the repo, and run the same command. The capture is labelled
`windows` automatically (under WSL2 it is labelled `linux`).

For the optional `--load` capture you also need `ffmpeg` with NVENC. The simplest
way to get it is winget, from PowerShell:

```powershell
winget install Gyan.FFmpeg
```

Then reopen your shell so the new `ffmpeg` is on `PATH`. NVENC works on essentially
all NVIDIA GPUs (AV1 encode needs an RTX 40-series or newer, but the collector
falls back to H.264/HEVC on its own).

## How developers/tests use this

**These captures are executable.** `cmd/fake-nvidia-smi` is a fake nvidia-smi
that replays a capture file as-is: it serves the recorded sections verbatim and
answers `--query-gpu`/`--query-compute-apps` for any field subset by projecting
columns out of the recorded CSV. The integration tests under `integration/` run
the real exporter against the fake for **every capture in this directory** and
compare the scraped metrics against a golden file, so contributing a capture
automatically extends the test suite. A new capture fails the suite until its
golden file is generated and reviewed:

```bash
go test ./integration/ -update   # generates integration/testdata/<capture>__<state>.metrics
git diff integration/testdata/   # eyeball what the exporter makes of the new capture
```

(Contributors don't have to do this — attaching the capture to an issue or PR is
enough, maintainers take it from there.)

The fake is also handy for local development without a GPU:

```bash
go build -o fake-nvidia-smi ./cmd/fake-nvidia-smi
go run ./cmd/nvidia_gpu_exporter --nvidia-smi-command \
  "./fake-nvidia-smi --capture testdata/captures/<file>.txt --state load"
```

Beyond that:

- Test field auto-detection against the `--help-query-gpu` sections of many
  drivers at once.
- Diff the same command across two captures to spot field renames or format drift.

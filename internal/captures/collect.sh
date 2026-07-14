#!/usr/bin/env bash
#
# collect.sh - capture raw `nvidia-smi` output for the nvidia_gpu_exporter project.
#
# Why: the exporter parses the text/CSV that `nvidia-smi` prints, and that output
# varies by GPU model, driver version, OS and load. Capturing real samples lets
# us develop and test the tool offline and reproduce bugs on hardware we don't
# own.
#
# Output: a single self-contained .txt file per machine, holding a metadata
# header followed by one clearly-separated section per command. That one file is
# the unit of contribution - attach it to a bug report, or commit it in a PR.
#
# Requirements (the floor): bash, awk, sed, and `nvidia-smi` on PATH.
# Optional: ffmpeg, for `--load` (capture realistic "under load" output).
#
# Usage:
#   ./collect.sh [options]
#
# Options:
#   --out DIR          Base output directory (default: this script's directory,
#                      i.e. internal/captures in a clone of the repo).
#   --nvidia-smi CMD   nvidia-smi invocation (default: nvidia-smi). May contain
#                      args. NOTE: OS/host metadata is read from wherever THIS
#                      script runs, so prefer running it on the GPU host.
#   --load             Also capture an "under load" state. Needs ffmpeg; spins up
#                      NVENC encode jobs, samples mid-load, then stops them.
#   --load-seconds N   Safety cap on load duration, in seconds (default: 45).
#                      Normally the load is stopped right after the load capture;
#                      this cap only matters if the script is interrupted.
#   --load-jobs N      Concurrent NVENC jobs, for multiple processes (default: 2).
#   --no-mask          Do NOT mask identifiers. NOT recommended for public sharing.
#   -h, --help         Show this help.
#
# Privacy: by default, identifiers that can fingerprint a machine (GPU UUID,
# serial, hostname) are replaced with format-preserving placeholders. Tests only
# care about the shape of the data, not the real values. Please still skim the
# file (especially process names) before sharing.

set -uo pipefail

# ---- defaults ---------------------------------------------------------------
# Default output dir is the directory this script lives in. In a clone of the
# repo that is internal/captures/, so the file lands where it belongs for a PR.
# When run from stdin (e.g. `ssh host bash -s`) the location is unknown, so fall
# back to the current directory and rely on --out.
if [ -n "${BASH_SOURCE:-}" ] && [ -f "${BASH_SOURCE}" ]; then
  OUT="$(cd "$(dirname "${BASH_SOURCE}")" && pwd)"
else
  OUT="."
fi
NVSMI="nvidia-smi"
WITH_LOAD=0
LOAD_SECONDS=45
LOAD_JOBS=2
MASK=1

# ---- args -------------------------------------------------------------------
while [ $# -gt 0 ]; do
  case "$1" in
    --out)
      OUT="$2"
      shift 2
      ;;
    --nvidia-smi)
      NVSMI="$2"
      shift 2
      ;;
    --load)
      WITH_LOAD=1
      shift
      ;;
    --load-seconds)
      LOAD_SECONDS="$2"
      shift 2
      ;;
    --load-jobs)
      LOAD_JOBS="$2"
      shift 2
      ;;
    --no-mask)
      MASK=0
      shift
      ;;
    -h | --help)
      sed -n '2,/^set -uo/p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      exit 2
      ;;
  esac
done

# nv runs nvidia-smi (NVSMI may contain args, so it is intentionally unquoted).
nv() { $NVSMI "$@"; }
have() { command -v "$1" > /dev/null 2>&1; }

cat << 'BANNER'
--------------------------------------------------------------------------------
 nvidia_gpu_exporter :: GPU output collector
--------------------------------------------------------------------------------
 What this does:
   * Runs a series of READ-ONLY `nvidia-smi` commands (queries, help, monitors).
     It never changes any GPU/system setting.
   * Writes ONE .txt file: a metadata header plus one section per command.
   * By default, MASKS fingerprinting identifiers (GPU UUID, serial, hostname)
     with format-preserving placeholders. Pass --no-mask to keep raw values.
 Attach that file to a bug report, or commit it in a pull request.
--------------------------------------------------------------------------------
BANNER

# ---- preflight: make sure the tools we need are present ---------------------
missing=
for t in awk sed tr paste head; do have "$t" || missing="$missing $t"; done
if [ -n "$missing" ]; then
  cat >&2 << EOF

error: missing required command(s):$missing
These are standard shell tools, normally preinstalled on Linux and inside WSL or
Git-Bash on Windows. Please install them and run this script again.
EOF
  exit 1
fi

if ! nv --version > /dev/null 2>&1; then
  cat >&2 << EOF

error: could not run '$NVSMI'.
This script reads everything from nvidia-smi, so it needs the NVIDIA driver and
nvidia-smi installed and on your PATH. To check, run:

    nvidia-smi

If that prints your GPU, re-run this script. If it does not, install the NVIDIA
driver first (nvidia-smi is bundled with it), then try again.
EOF
  exit 1
fi

if [ "$WITH_LOAD" -eq 1 ] && ! have ffmpeg; then
  echo "note: --load needs ffmpeg, which was not found; capturing the idle state only." >&2
  echo "      (install ffmpeg if you also want an 'under load' sample.)" >&2
  WITH_LOAD=0
fi

# ---- helpers ----------------------------------------------------------------

# slug: lowercase, collapse runs of non-alphanumerics to '-', trim. For names.
slug() {
  printf '%s' "$1" | tr '[:upper:]' '[:lower:]' |
    sed -e 's/[^a-z0-9]\{1,\}/-/g' -e 's/^-//' -e 's/-$//'
}

# pathsafe: keep a string usable as a path segment, preserving '.', '_', '-'
# (so arch stays "x86_64" and a driver stays "595.71.05"). Other chars -> '-'.
pathsafe() {
  printf '%s' "$1" | sed -e 's/[^A-Za-z0-9._-]\{1,\}/-/g' -e 's/^-//' -e 's/-$//'
}

# first non-empty CSV value for a query field.
q1() { nv --query-gpu="$1" --format=csv,noheader 2> /dev/null | head -1 | sed 's/^ *//;s/ *$//'; }

# Derive a query field list the same way the exporter does: a field name is a
# `"name"` token on its own line, preceded by a blank line. POSIX awk only.
derive_fields() {
  nv "$1" 2> /dev/null | awk '
    /^[[:space:]]*$/ { blank=1; next }
    blank && /^"/    { s=$0; sub(/^"/,"",s); sub(/".*/,"",s); print s }
    { blank=0 }'
}

# section: append a separated section to OUTFILE, running `nvidia-smi <args>` and
# capturing its combined output. A failing command (unsupported feature) is data.
section() {
  local label="$1"
  shift
  {
    printf '\n\n'
    printf '################################################################################\n'
    printf '# %s\n' "$label"
    printf '# $ %s %s\n' "$NVSMI" "$*"
    printf '################################################################################\n'
    nv "$@" 2>&1
    local rc=$?
    [ "$rc" -ne 0 ] && printf '\n# (exit code: %d)\n' "$rc"
  } >> "$OUTFILE"
}

# emit_state: all the load-dependent commands for one state (idle/load).
emit_state() {
  local st="$1"
  section "$st :: nvidia-smi (default table)"
  section "$st :: query (-q)" -q
  section "$st :: query-gpu (csv, what the exporter parses)" --query-gpu="$GPU_FIELDS" --format=csv
  section "$st :: query-compute-apps (per-process)" --query-compute-apps="$CA_FIELDS" --format=csv
  section "$st :: query-accounted-apps" --query-accounted-apps="$AA_FIELDS" --format=csv
  [ -n "$RP_FIELDS" ] && section "$st :: query-retired-pages" --query-retired-pages="$RP_FIELDS" --format=csv
  section "$st :: pmon (per-process monitor)" pmon -c 5
  section "$st :: dmon (per-device monitor, incl. PCIe rx/tx)" dmon -c 5
}

# ---- metadata ---------------------------------------------------------------
OS_KERNEL="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
KERNEL_REL="$(uname -r 2> /dev/null || echo unknown)"
OS_PRETTY="unknown"
case "$OS_KERNEL" in
  mingw* | msys* | cygwin*)
    # Git-Bash / MSYS2 / Cygwin all run on Windows but uname reports a kernel
    # like "mingw64_nt-10.0-22621" and an MSYS runtime version. Normalize to a
    # clean "windows" label and read the real Windows version via cmd's `ver`.
    OS_KERNEL="windows"
    WIN_VER="$(cmd //c ver 2> /dev/null | tr -d '\r' | tr '\n' ' ' | tr -s ' ' | sed -e 's/^ *//' -e 's/ *$//')"
    KERNEL_REL="$(printf '%s' "$WIN_VER" | sed -n 's/.*\[Version \([0-9.]*\)\].*/\1/p')"
    [ -n "$KERNEL_REL" ] || KERNEL_REL="unknown"
    OS_PRETTY="$(printf '%s' "$WIN_VER" | sed -e 's/ *\[Version[^]]*\]//')"
    [ -n "$OS_PRETTY" ] || OS_PRETTY="Windows"
    ;;
  darwin)
    OS_PRETTY="macOS $(sw_vers -productVersion 2> /dev/null)"
    ;;
  *)
    if [ -r /etc/os-release ]; then
      # shellcheck disable=SC1091
      OS_PRETTY="$(
        . /etc/os-release 2> /dev/null
        printf '%s' "${PRETTY_NAME:-$NAME}"
      )"
    fi
    ;;
esac

# The model and driver are the only values derived from nvidia-smi, and only
# because the output filename is built from them. Everything else nvidia-smi
# reports stays in the raw sections below instead of being re-parsed into the
# header: re-parsing is what drifts and breaks when a driver renames a field.
DRIVER_VER="$(q1 driver_version)"
[ -n "$DRIVER_VER" ] || DRIVER_VER="unknown"
GPU_NAME="$(q1 name)"
[ -n "$GPU_NAME" ] || GPU_NAME="unknown-gpu"

# All keys live in one flat filename: <os>-<arch>__<model>__<driver>.txt
OUTFILE="$OUT/$(pathsafe "${OS_KERNEL}-${ARCH}")__$(slug "$GPU_NAME")__$(pathsafe "$DRIVER_VER").txt"
mkdir -p "$OUT"
echo ">> writing: $OUTFILE"

# Field lists (derived, future-proof).
GPU_FIELDS="$(derive_fields --help-query-gpu | paste -sd, -)"
CA_FIELDS="timestamp,gpu_name,gpu_bus_id,gpu_serial,gpu_uuid,pid,process_name,used_gpu_memory"
AA_FIELDS="timestamp,gpu_name,gpu_bus_id,gpu_serial,gpu_uuid,pid,gpu_util,mem_util,max_memory_usage,time"
RP_FIELDS="$(derive_fields --help-query-retired-pages | paste -sd, -)"

# ---- load plan --------------------------------------------------------------
# Only work out the description string here; the jobs themselves are started
# later, right before the load capture, so they cannot bleed into the idle
# capture that comes first.
LOAD_METHOD="none"
load_pids=""
if [ "$WITH_LOAD" -eq 1 ]; then
  # The codec loop below tries at most three codecs, so the job count caps at 3.
  load_njobs="$LOAD_JOBS"
  [ "$load_njobs" -gt 3 ] && load_njobs=3
  LOAD_METHOD="ffmpeg NVENC x${load_njobs} (1080p120 testsrc2)"
fi

# ---- header -----------------------------------------------------------------
{
  echo "################################################################################"
  echo "# nvidia_gpu_exporter GPU capture"
  echo "# https://github.com/utkuozdemir/nvidia_gpu_exporter - see internal/captures/README.md"
  echo "################################################################################"
  echo "# The GPU model and driver are in the filename. Every other nvidia-smi"
  echo "# value is in the raw sections below, not re-parsed here. This header only"
  echo "# carries what nvidia-smi does not report: collection time, mask status,"
  echo "# host OS (from uname), and the synthetic load used."
  echo "collected_at:   $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "masked:         $([ "$MASK" -eq 1 ] && echo yes || echo 'NO - review before sharing!')"
  echo "os:             $OS_KERNEL ($OS_PRETTY)"
  echo "kernel:         $KERNEL_REL"
  echo "arch:           $ARCH"
  echo "load:           $LOAD_METHOD"
} > "$OUTFILE"

# ---- capabilities (state-independent, depend on driver/model) ---------------
section "capabilities :: version" --version
section "capabilities :: help (-h)" -h
section "capabilities :: help-query-gpu" --help-query-gpu
section "capabilities :: help-query-compute-apps" --help-query-compute-apps
section "capabilities :: help-query-accounted-apps" --help-query-accounted-apps
section "capabilities :: help-query-retired-pages" --help-query-retired-pages
section "capabilities :: list (-L)" -L
section "capabilities :: topo -m" topo -m
section "capabilities :: nvlink -s" nvlink -s

# Best-effort NVML symbol inventory: the exported function names of the
# driver library, used by the exporter's driver-drift tests. Skipped quietly
# when the library or an ELF reader is not available (the capture stays
# useful without it).
nvml_symbols_section() {
  local lib=""
  if command -v ldconfig > /dev/null 2>&1; then
    lib="$(ldconfig -p 2> /dev/null | awk '/libnvidia-ml\.so\.1/{print $NF; exit}')"
  fi
  [ -z "$lib" ] && for candidate in /usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1 \
    /usr/lib64/libnvidia-ml.so.1 /lib/x86_64-linux-gnu/libnvidia-ml.so.1; do
    [ -e "$candidate" ] && lib="$candidate" && break
  done
  [ -z "$lib" ] && return 0

  local symbols="" tool=""
  if command -v readelf > /dev/null 2>&1; then
    tool="readelf"
    symbols="$(readelf --dyn-syms --wide "$lib" 2> /dev/null |
      awk '$4=="FUNC" && $7!="UND" {print $8}' | grep '^nvml' | sed 's/@@.*//' | sort -u)"
  elif command -v nm > /dev/null 2>&1; then
    tool="nm"
    symbols="$(nm -D --defined-only "$lib" 2> /dev/null |
      awk '$2=="T" {print $3}' | grep '^nvml' | sed 's/@@.*//' | sort -u)"
  fi
  [ -z "$symbols" ] && return 0

  local sha=""
  command -v sha256sum > /dev/null 2>&1 && sha="$(sha256sum "$lib" | awk '{print $1}')"

  {
    printf '\n\n'
    printf '################################################################################\n'
    printf '# capabilities :: nvml-symbols\n'
    printf '# $ %s %s | awk ... | grep ^nvml | sort -u\n' "$tool" "$lib"
    [ -n "$sha" ] && printf '# library sha256: %s\n' "$sha"
    printf '################################################################################\n'
    printf '%s\n' "$symbols"
  } >> "$OUTFILE"
}
nvml_symbols_section
{
  printf '\n\n################################################################################\n'
  printf '# capabilities :: query-gpu field list (derived, used for query-gpu above)\n'
  printf '################################################################################\n'
  printf '%s\n' "$GPU_FIELDS" | tr ',' '\n'
} >> "$OUTFILE"

# ---- idle, then load --------------------------------------------------------
echo ">> capturing idle state"
emit_state "idle"

if [ "$WITH_LOAD" -eq 1 ]; then
  echo ">> starting $LOAD_JOBS NVENC load job(s)"
  i=0
  for codec in hevc_nvenc h264_nvenc av1_nvenc; do
    [ "$i" -ge "$LOAD_JOBS" ] && break
    # No -t: encode until killed, so the GPU stays busy for the whole load
    # capture no matter how fast this card's NVENC is (a fast card would finish
    # a fixed-length clip long before the capture is done).
    ffmpeg -hide_banner -loglevel error \
      -f lavfi -i "testsrc2=size=1920x1080:rate=120" \
      -c:v "$codec" -preset p7 -f null - > /dev/null 2>&1 &
    load_pids="$load_pids $!"
    i=$((i + 1))
  done
  # Watchdog: stop the load after the safety cap even if this script is
  # interrupted, so a stray ffmpeg can never run forever. It is detached, so it
  # survives the parent and still fires.
  # shellcheck disable=SC2086
  (sleep "$LOAD_SECONDS" && kill $load_pids 2> /dev/null) &
  watchdog_pid=$!

  sleep 6 # let clocks/power/encoder ramp before sampling
  echo ">> capturing load state"
  emit_state "load"

  # shellcheck disable=SC2086
  kill $load_pids 2> /dev/null
  kill "$watchdog_pid" 2> /dev/null
  wait 2> /dev/null
fi

# ---- masking ----------------------------------------------------------------
# Replace fingerprinting identifiers with format-preserving placeholders. One
# file, so a couple of sed passes. Done last so real values are never written out.

# sed_inplace edits $OUTFILE in place, portably: GNU `sed -i` and BSD/macOS
# `sed -i ''` disagree on syntax, so use a temp file and mv instead.
sed_inplace() {
  sed "$@" "$OUTFILE" > "$OUTFILE.tmp" && mv "$OUTFILE.tmp" "$OUTFILE"
}

if [ "$MASK" -eq 1 ]; then
  echo ">> masking identifiers"
  i=0
  nv --query-gpu=uuid --format=csv,noheader 2> /dev/null | sed 's/^ *//;s/ *$//' | while IFS= read -r u; do
    [ -n "$u" ] || continue
    ph="$(printf 'GPU-%08d-0000-0000-0000-%012d' "$i" "$i")"
    sed_inplace \
      -e "s/${u}/${ph}/g" \
      -e "s/$(printf '%s' "$u" | tr '[:upper:]' '[:lower:]')/$(printf '%s' "$ph" | tr '[:upper:]' '[:lower:]')/g"
    i=$((i + 1))
  done
  nv --query-gpu=serial --format=csv,noheader 2> /dev/null | sed 's/^ *//;s/ *$//' | while IFS= read -r s; do
    case "$s" in "" | "[N/A]" | "N/A") continue ;; esac
    sed_inplace "s/${s}/0000000000000/g"
  done
  HOSTN="$(hostname 2> /dev/null || uname -n 2> /dev/null)"
  [ -n "${HOSTN:-}" ] && sed_inplace "s/${HOSTN}/redacted-host/g"
fi

cat << EOF

--------------------------------------------------------------------------------
 Done. Identifiers were $([ "$MASK" -eq 1 ] && echo 'masked' || echo 'NOT masked').
 Wrote:
   $OUTFILE

 To help the project, either:
   * attach that file to a GitHub issue, or
   * commit it and open a pull request:
       git add "$OUTFILE"
       git commit -m "capture: $GPU_NAME ($DRIVER_VER, ${OS_KERNEL}-${ARCH})"

 Thank you for helping support more GPUs / drivers / platforms!
--------------------------------------------------------------------------------
EOF

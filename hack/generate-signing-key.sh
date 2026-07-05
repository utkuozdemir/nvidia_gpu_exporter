#!/usr/bin/env bash
#
# Generates the repo-wide GPG signing key and wires up the public parts.
#
# This key signs the released helm chart provenance files and the release
# checksums. Container images and the OCI chart are signed keyless with cosign
# and need no key, so this is the only signing key the project has.
#
# What it does:
#   - generates an RSA-4096, sign-only, non-expiring key in a throwaway keyring
#   - uploads the private key and passphrase as the GPG_PRIVATE_KEY and
#     GPG_PASSPHRASE repository secrets, and deletes the old CHART_SIGNING_*
#     secrets if present
#   - patches the new fingerprint into the chart's signKey annotation
#   - drops the private key and passphrase into a fresh temporary directory for
#     you to move into your password manager, then delete
#
# The public fingerprint is the only thing that ends up committed. The public
# key itself is exported and published to the Helm repository by CI at release
# time, so it does not live in the repository tree.
#
# Re-run this script to rotate the key. Old releases stay verifiable against the
# old key.

set -euo pipefail

repo="utkuozdemir/nvidia_gpu_exporter"
key_uid="nvidia_gpu_exporter release signing <utkuozdemir@gmail.com>"
chart_yaml="charts/nvidia-gpu-exporter/Chart.yaml"
out_dir="$(mktemp -d "${TMPDIR:-/tmp}/nvidia_gpu_exporter-signing.XXXXXX")"

for tool in gpg gh openssl awk; do
  if ! command -v "$tool" > /dev/null 2>&1; then
    echo "error: required tool not found: $tool" >&2
    exit 1
  fi
done

cd "$(git rev-parse --show-toplevel)"

if [ ! -f "$chart_yaml" ]; then
  echo "error: $chart_yaml not found; run from the repository" >&2
  exit 1
fi

if ! grep -Eq '^[[:space:]]*fingerprint: [0-9A-Fa-f]{40}$' "$chart_yaml"; then
  echo "error: could not find a fingerprint line to update in $chart_yaml" >&2
  exit 1
fi

if ! gh auth status > /dev/null 2>&1; then
  echo "error: gh is not authenticated; run 'gh auth login' first" >&2
  exit 1
fi

# throwaway keyring so nothing touches the real ~/.gnupg
GNUPGHOME="$(mktemp -d "${TMPDIR:-/tmp}/nvidia_gpu_exporter-gnupg.XXXXXX")"
export GNUPGHOME
trap 'rm -rf "$GNUPGHOME"' EXIT
chmod 700 "$GNUPGHOME"

# let gpg take the passphrase from --passphrase instead of a pinentry prompt
printf 'allow-loopback-pinentry\n' > "$GNUPGHOME/gpg-agent.conf"
gpgconf --kill gpg-agent > /dev/null 2>&1 || true

passphrase="$(openssl rand -base64 32)"

echo "Generating an RSA-4096 sign-only key with no expiry ..."
gpg --batch --pinentry-mode loopback --passphrase "$passphrase" \
  --quick-generate-key "$key_uid" rsa4096 sign never

fingerprint="$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr:/{print $10; exit}')"
if [ -z "$fingerprint" ]; then
  echo "error: failed to read the generated key fingerprint" >&2
  exit 1
fi

umask 077
# --export-secret-keys needs the passphrase to unlock the key; --export does not
gpg --batch --pinentry-mode loopback --passphrase "$passphrase" \
  --armor --export-secret-keys "$fingerprint" > "$out_dir/private-key.asc"
gpg --armor --export "$fingerprint" > "$out_dir/pubkey.asc"
printf '%s' "$passphrase" > "$out_dir/passphrase.txt"
printf '%s\n' "$fingerprint" > "$out_dir/fingerprint.txt"

if [ ! -s "$out_dir/private-key.asc" ]; then
  echo "error: exported private key is empty; aborting before touching secrets" >&2
  exit 1
fi

echo "Uploading the GPG_PRIVATE_KEY and GPG_PASSPHRASE secrets ..."
gh secret set -R "$repo" GPG_PRIVATE_KEY < "$out_dir/private-key.asc"
printf '%s' "$passphrase" | gh secret set -R "$repo" GPG_PASSPHRASE

echo "Removing the old CHART_SIGNING_* secrets if they exist ..."
gh secret delete -R "$repo" CHART_SIGNING_KEY > /dev/null 2>&1 || true
gh secret delete -R "$repo" CHART_SIGNING_PASSPHRASE > /dev/null 2>&1 || true

echo "Writing the fingerprint into $chart_yaml ..."
tmp_chart="$(mktemp)"
sed -E "s/^([[:space:]]*fingerprint: )[0-9A-Fa-f]{40}$/\1${fingerprint}/" \
  "$chart_yaml" > "$tmp_chart"
mv "$tmp_chart" "$chart_yaml"

cat << EOF

Done. Fingerprint: ${fingerprint}

The fingerprint is already written into $chart_yaml. The public key is
published to the Helm repository by CI at release time, so it stays out of the
repository tree.

Temporary key material is in:
  $out_dir

Handle it now:
  1. Move these into your password manager:
       $out_dir/private-key.asc
       $out_dir/passphrase.txt
  2. Then delete the whole temporary directory:
       rm -rf $out_dir

The repository secrets GPG_PRIVATE_KEY and GPG_PASSPHRASE are what CI uses.
EOF

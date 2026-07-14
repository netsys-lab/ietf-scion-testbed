#!/bin/bash
# Generate the SCION-in-a-Box hev3 demo CA and the per-name server certs the
# hev3-server listeners present (web.scion, web2.scion). Output lands in
# ansible/files/hev3-ca/ and is DELIBERATELY committed — this is throwaway
# testbed-only key material, never trust it for anything real.
#
# Idempotent: an existing ca.pem is reused (not regenerated); an existing
# <name>/cert.pem is left alone. Delete a file to force it to be recreated.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/ansible/files/hev3-ca"
CA_CN="SCION-in-a-Box hev3 CA"
NAMES=(web.scion web2.scion welcome.scion)

mkdir -p "$OUT"

CA_PEM="$OUT/ca.pem"
CA_KEY="$OUT/ca.key"

if [ -f "$CA_PEM" ]; then
  echo "CA exists, reusing $CA_PEM"
else
  echo "creating CA -> $CA_PEM"
  openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
    -keyout "$CA_KEY" -out "$CA_PEM" \
    -subj "/CN=$CA_CN"
fi

for name in "${NAMES[@]}"; do
  dir="$OUT/$name"
  cert="$dir/cert.pem"
  key="$dir/key.pem"
  if [ -f "$cert" ]; then
    echo "cert for $name exists, skipping"
    continue
  fi
  echo "creating cert for $name -> $cert"
  mkdir -p "$dir"
  csr="$(mktemp)"
  trap 'rm -f "$csr"' EXIT
  openssl req -newkey rsa:2048 -sha256 -nodes \
    -keyout "$key" -out "$csr" \
    -subj "/CN=$name"
  openssl x509 -req -in "$csr" -sha256 -days 730 \
    -CA "$CA_PEM" -CAkey "$CA_KEY" -CAcreateserial \
    -extfile <(printf 'subjectAltName=DNS:%s\nbasicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n' "$name") \
    -out "$cert"
  rm -f "$csr"
  trap - EXIT
done

echo "done. contents of $OUT:"
find "$OUT" -type f | sort

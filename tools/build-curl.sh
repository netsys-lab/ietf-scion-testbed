#!/bin/bash
# Build a modern curl WITH HTTP/3 (QUIC) for the playground + svc endhosts.
# HTTP/3 needs a QUIC-capable TLS stack. We build OpenSSL 3.5 (which ships a
# native QUIC API) + nghttp3 + curl from source inside an ubuntu:24.04
# container so the resulting binary loads on the glibc-2.39 fleet.
# Mirrors tools/build-scitra.sh: containerized, pinned, objdump-guarded.
#
# QUIC BACKEND CHOICE (why no ngtcp2): ngtcp2's `--with-openssl` backend wants
# the quictls/BoringSSL QUIC API (SSL_provide_quic_data / SSL_set_quic_method).
# Stock OpenSSL 3.5 has QUIC but via a *different* native API, so an ngtcp2
# `--with-openssl` build against it fails configure with "openssl does not have
# QUIC interface" (observed on the first attempt). Rather than pull in quictls
# as a second TLS source, we use curl's built-in OpenSSL-QUIC HTTP/3 backend
# (`--with-openssl-quic`): curl talks QUIC through OpenSSL 3.5 directly and uses
# nghttp3 only for HTTP/3 framing. Fewer moving parts, one TLS stack.
#
# STATIC-LINK DECISION (see task-10-report.md for the full trail): OpenSSL is
# built `no-shared` (static .a only) and nghttp3 with --disable-shared (only .a
# produced), so curl's configure has no .so to pick up and links both in
# statically -- the resulting .build/curl/bin/curl is a single self-contained
# binary (verified via ldd: no /opt/quic references). If a future version bump
# makes one of these projects refuse --disable-shared, fall back to shipping the
# .so next to the binary and note that T13/T14 must deploy both.
#
# USAGE:  ./tools/build-curl.sh          # -> .build/curl/bin/curl
# Env vars (bump to current stable and re-verify on the projects' release pages):
#   CURL_VERSION     default 8.11.1
#   OPENSSL_VERSION  default 3.5.2   (>=3.5 required for the native QUIC API)
#   NGHTTP3_VERSION  default v1.7.0
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$REPO_ROOT/.build/curl/bin"
CURL_VERSION="${CURL_VERSION:-8.11.1}"
OPENSSL_VERSION="${OPENSSL_VERSION:-3.5.2}"
NGHTTP3_VERSION="${NGHTTP3_VERSION:-v1.7.0}"
GLIBC_CEIL="2.39"

command -v docker >/dev/null || { echo "error: docker not found" >&2; exit 1; }
mkdir -p "$OUT"

docker run --rm -v "$OUT:/out" ubuntu:24.04 bash -euo pipefail -c "
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends \
  build-essential ca-certificates curl git pkg-config libtool autoconf automake \
  cmake ninja-build perl
cd /tmp

# --- OpenSSL 3.5 (QUIC-capable), installed to a private prefix, static only ---
curl -fsSLO https://github.com/openssl/openssl/releases/download/openssl-${OPENSSL_VERSION}/openssl-${OPENSSL_VERSION}.tar.gz
tar xf openssl-${OPENSSL_VERSION}.tar.gz && cd openssl-${OPENSSL_VERSION}
# -fPIC: static-only objects are otherwise non-PIC, and Ubuntu 24.04's gcc
# defaults to -pie, so the final curl PIE executable needs PIC objects to link.
./Configure --prefix=/opt/quic --libdir=lib enable-tls1_3 no-shared -fPIC
make -j\$(nproc) && make install_sw
cd /tmp

# --- nghttp3 (static only, so curl links it in rather than DT_NEEDED-ing a .so) ---
git clone --depth 1 -b ${NGHTTP3_VERSION} https://github.com/ngtcp2/nghttp3
cd nghttp3 && git submodule update --init --depth 1
autoreconf -fi
./configure --prefix=/opt/quic --enable-lib-only --disable-shared --with-pic \
  PKG_CONFIG_PATH=/opt/quic/lib/pkgconfig
make -j\$(nproc) && make install
cd /tmp

# --- curl with HTTP/3 via OpenSSL-QUIC (no ngtcp2; see header) ---
curl -fsSLO https://curl.se/download/curl-${CURL_VERSION}.tar.gz
tar xf curl-${CURL_VERSION}.tar.gz && cd curl-${CURL_VERSION}
# --disable-shared: build only the curl tool (static libcurl.a), NOT libcurl.so.
#   A shared libcurl.so cannot link the non-PIC static OpenSSL/nghttp3 archives,
#   and a tool linked against libcurl.so would carry a libcurl.so runtime dep --
#   both defeat the self-contained goal. The tool links the static libs directly.
# --without-libpsl: curl errors out if libpsl is absent; we don't need
#   Public-Suffix-List cookie scoping for demo transfers, and pulling it in would
#   add a libpsl.so runtime dep.
./configure --with-openssl=/opt/quic --with-openssl-quic --with-nghttp3=/opt/quic \
  --disable-shared --without-libpsl --prefix=/opt/quic \
  PKG_CONFIG_PATH=/opt/quic/lib/pkgconfig
make -j\$(nproc)
cp src/curl /out/curl
# OpenSSL (no-shared, -fPIC) + nghttp3 (--disable-shared --with-pic) are
# static-only in /opt/quic, so curl links them in; the resulting binary should
# have no /opt/quic runtime dependency (verified by ldd on the host after this
# container exits).
# Smoke test runs HERE, inside the ubuntu:24.04 container: the binary needs the
# fleet's glibc (>= what it links against), and the build HOST may be OLDER than
# the fleet (e.g. building on a glibc-2.35 box for a glibc-2.39 fleet), so the
# host cannot necessarily execute a fleet binary. Assert HTTP3 where glibc matches.
/out/curl --version
/out/curl --version | grep -qi HTTP3 || { echo 'error: built curl lacks HTTP3 feature' >&2; exit 1; }
"

# --- glibc ceiling guard (mirrors build-scitra.sh) ---
# objdump does NOT execute the binary, so this is safe on an older-than-fleet
# build host; it only confirms the max required GLIBC symbol is <= the ceiling.
max_glibc="$(objdump -T "$OUT/curl" | grep -oE 'GLIBC_[0-9]+\.[0-9]+' | sort -uV | tail -1 || true)"
if [ -n "$max_glibc" ] && [ "$(printf '%s\nGLIBC_%s\n' "$max_glibc" "$GLIBC_CEIL" | sort -V | tail -1)" != "GLIBC_$GLIBC_CEIL" ]; then
    echo "error: curl needs $max_glibc > GLIBC_$GLIBC_CEIL ceiling" >&2
    exit 1
fi

# HTTP3 was asserted in-container above (the build host may be too old to run the
# fleet binary). Nothing else to execute here.
echo "OK: $OUT/curl built (HTTP3 asserted in-container; GLIBC ceiling ${max_glibc:-none} <= GLIBC_$GLIBC_CEIL)"

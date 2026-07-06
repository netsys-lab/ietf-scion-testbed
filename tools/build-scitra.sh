#!/bin/bash
# Build scitra-tun (SCION<->IPv6 translator) and scion2ip from lschulz/scion-cpp
# for the Debian 12 playground containers (CT210-213, glibc 2.36).
#
# WHY A DOCKER BUILD:
#   scion-cpp needs a C++23 toolchain (gcc >= 13) plus Boost >= 1.83 / gRPC >=
#   1.51 / protobuf >= 3.21. No single apt host in this testbed provides those
#   at a glibc old enough to also run on Debian 12: the dev box is Ubuntu 22.04
#   (gcc 11, no C++23) and the Proxmox host is Debian 13 (glibc 2.41 -> a binary
#   built there references __isoc23_* @GLIBC_2.38 and will NOT run on the
#   Debian 12 containers). So we build inside gcc:13-bookworm (gcc 13.4 + glibc
#   2.36 == the CT210/213 runtime), pull deps via vcpkg (static libs), and link
#   libstdc++/libgcc statically with MARCH='' (portable amd64 baseline).
#
# RUNTIME DEPENDENCIES on the target (Debian 12): glibc 2.36, libmnl0, libcap2
#   (libcap.so.2 + libpsx.so.2), libtinfo6, and libncurses6 -- the last one is
#   NOT in a minimal container and provides libncurses.so.6 + libform.so.6 that
#   the always-linked imtui TUI code needs (scitra-tun won't even start without
#   them). Deploy step (A8) must `apt-get install -y libncurses6`.
#   The C++ runtime (libstdc++/libgcc) is static, so it is NOT a runtime dep.
#
# USAGE:
#   ./tools/build-scitra.sh                    # builds .build/scitra/bin/{scitra-tun,scion2ip}
#   SCION_CPP=/path/to/scion-cpp ./tools/build-scitra.sh
# Env vars:
#   SCION_CPP          scion-cpp git checkout (default /home/tony/lshulz/scion-cpp)
#   BUILDER_IMAGE      build container (default gcc:13-bookworm -- keep glibc <= 2.36)
#   SCITRA_BUILD_WORK  persistent vcpkg/cmake cache (default ~/.cache/scitra-work)
# Requires: docker, and network access from the build container (vcpkg fetches).
# First run ~30-45 min (grpc/boost/protobuf from source); reruns are cached.
#
# NATIVE (no-Docker) ALTERNATIVE -- only if your host is Ubuntu 24.04 / Debian 13
# AND you do NOT need Debian 12 compatibility. Install the README's deps:
#   sudo apt-get install build-essential cmake ninja-build pkg-config \
#     libboost-dev libboost-json-dev libc-ares-dev libcap-dev libgrpc++-dev \
#     libmnl-dev libncurses-dev libprotobuf-dev libre2-dev libtomlplusplus-dev \
#     pandoc protobuf-compiler protobuf-compiler-grpc
#   git -C "$SCION_CPP" submodule update --init --recursive
#   cmake -G 'Ninja Multi-Config' -S "$SCION_CPP" -B build -DMARCH='' -DRELEASE=YES
#   cmake --build build --config Release --target scitra-tun scion2ip
# (A binary built this way links glibc > 2.36 and will fail on the Debian 12
#  containers with "GLIBC_2.3x not found".)
set -euo pipefail

SCION_CPP="${SCION_CPP:-/home/tony/lshulz/scion-cpp}"
BUILDER_IMAGE="${BUILDER_IMAGE:-gcc:13-bookworm}"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$REPO_ROOT/.build/scitra/bin"
WORK="${SCITRA_BUILD_WORK:-$HOME/.cache/scitra-work}"

if [ ! -d "$SCION_CPP/.git" ]; then
    echo "error: SCION_CPP='$SCION_CPP' is not a git checkout of scion-cpp" >&2
    echo "       git clone https://github.com/lschulz/scion-cpp '$SCION_CPP'" >&2
    exit 1
fi
command -v docker >/dev/null || { echo "error: docker not found" >&2; exit 1; }

# scion-cpp vendors deps as submodules (CLI11, spdlog, imtui, asio-grpc, gtest)
git -C "$SCION_CPP" submodule update --init --recursive

mkdir -p "$OUT" "$WORK/triplets" "$WORK/vcpkg-cache"

# Release-only, static-lib vcpkg triplet (half the build time of debug+release).
cat > "$WORK/triplets/x64-linux-release.cmake" <<'TRIPLET'
set(VCPKG_TARGET_ARCHITECTURE x64)
set(VCPKG_CRT_LINKAGE dynamic)
set(VCPKG_LIBRARY_LINKAGE static)
set(VCPKG_CMAKE_SYSTEM_NAME Linux)
set(VCPKG_BUILD_TYPE release)
TRIPLET

# Steps that run inside the Debian 12 build container.
cat > "$WORK/incontainer.sh" <<'INNER'
#!/bin/bash
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

echo "[1/5] apt build prerequisites"
apt-get update -qq
apt-get install -y --no-install-recommends \
  ninja-build pkg-config git curl zip unzip tar ca-certificates \
  autoconf automake libtool autoconf-archive python3 bison flex \
  linux-libc-dev libmnl-dev libcap-dev libncurses-dev >/dev/null

echo "[2/5] modern cmake (scion-cpp needs >=3.28; bookworm apt only ships 3.25)"
CMAKE_VER=3.31.6
if [ ! -x "/work/cmake/bin/cmake" ]; then
  mkdir -p /work/cmake
  curl -fsSL "https://github.com/Kitware/CMake/releases/download/v${CMAKE_VER}/cmake-${CMAKE_VER}-linux-x86_64.tar.gz" \
    | tar xz -C /work/cmake --strip-components=1
fi
export PATH=/work/cmake/bin:$PATH
cmake --version | head -1

echo "[3/5] vcpkg (port versions pinned by scion-cpp/vcpkg-configuration.json)"
export VCPKG_ROOT=/work/vcpkg
if [ ! -x "$VCPKG_ROOT/vcpkg" ]; then
  rm -rf "$VCPKG_ROOT"
  git clone https://github.com/microsoft/vcpkg "$VCPKG_ROOT"
  "$VCPKG_ROOT/bootstrap-vcpkg.sh" -disableMetrics
fi
export VCPKG_DEFAULT_BINARY_CACHE=/work/vcpkg-cache
mkdir -p "$VCPKG_DEFAULT_BINARY_CACHE"
export VCPKG_MAX_CONCURRENCY="$(nproc)"

echo "[4/5] configure + build (Release; static libstdc++/libgcc; MARCH='')"
# Protobuf_FOUND/gRPC_FOUND forced ON: scion-cpp's CMakeLists checks these
# capital-cased vars, but find_package(protobuf/grpc CONFIG) sets the
# lowercase-name vars, so it otherwise drops into a pkg-config fallback that
# does add_executable(protobuf::protoc IMPORTED) -- which collides with the
# imported target vcpkg already defines. Forcing them keeps the vcpkg config
# path (protobuf::libprotobuf, gRPC::grpc, gRPC::grpc++).
cmake -G 'Ninja Multi-Config' -S /src -B /work/build \
  -DCMAKE_TOOLCHAIN_FILE="$VCPKG_ROOT/scripts/buildsystems/vcpkg.cmake" \
  -DVCPKG_OVERLAY_TRIPLETS=/work/triplets \
  -DVCPKG_TARGET_TRIPLET=x64-linux-release \
  -DVCPKG_HOST_TRIPLET=x64-linux-release \
  -DMARCH='' \
  -DRELEASE=YES \
  -DProtobuf_FOUND=ON \
  -DgRPC_FOUND=ON \
  -DCMAKE_EXE_LINKER_FLAGS='-static-libstdc++ -static-libgcc'
cmake --build /work/build --config Release --target scitra-tun scion2ip -j "$(nproc)"

echo "[5/5] collect"
cp "$(find /work/build -type f -name scitra-tun -perm -u+x | head -1)" /out/scitra-tun
cp "$(find /work/build -type f -name scion2ip  -perm -u+x | head -1)" /out/scion2ip
chmod 0755 /out/scitra-tun /out/scion2ip
echo "=== ldd scitra-tun ==="; ldd /out/scitra-tun || true
echo "=== max GLIBC symbol version (must be <= 2.36) ==="
objdump -T /out/scitra-tun 2>/dev/null | grep -oE 'GLIBC_[0-9.]+' | sort -V | uniq | tail -3
INNER

echo "Building scitra-tun + scion2ip in $BUILDER_IMAGE from $SCION_CPP -> $OUT"
echo "(vcpkg fetches/builds deps on first run; this takes a while)"
docker run --rm \
  -v "$SCION_CPP":/src \
  -v "$WORK":/work \
  -v "$OUT":/out \
  "$BUILDER_IMAGE" bash /work/incontainer.sh

echo "Built:"
ls -la "$OUT"

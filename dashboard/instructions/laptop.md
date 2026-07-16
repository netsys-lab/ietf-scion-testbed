# Laptop — any OS (VM or native Linux)

Bring your own machine onto the SCION testbed as a real endhost in one of
ASes 1-152, 1-155, 1-158, 1-161 — no building from source. A guided
bootstrap script installs prebuilt SCION + scitra packages, brings up your
WireGuard tunnel, and verifies the whole thing, on any Ubuntu 24.04 —
including a VM on Windows or macOS.

## 1. Get an Ubuntu 24.04 (pick one)

- **Windows / macOS / Linux — Multipass VM** (recommended):
  install [Multipass](https://canonical.com/multipass/install), then

  ```
  multipass launch 24.04 --name scion
  multipass shell scion
  ```

  ⚠ **Apple Silicon Macs**: the prebuilt packages are amd64-only, and your
  VM is arm64 — see the appendix at the bottom (or just use the browser
  playground, `playground.md`).

- **Windows — WSL2**: an Ubuntu 24.04 WSL distro works too; the script
  detects WSL and tells you the two `/etc/wsl.conf` lines it needs
  (`generateHosts = false`, `generateResolvConf = false`, then reboot the
  distro before running it).

- **Native Ubuntu 24.04**: skip the VM, run everything directly.

## 2. Claim your conf (and the TLS CA)

On this page (`/join`), enter the booth code and claim a WireGuard conf —
one conf tunnels the whole testbed; the download is named
`scion-ietf126-as<N>.conf`. Also grab **"Download TLS CA"** while you're
here (for `curl https://…scion/` later). Copy both into the VM:

```
multipass transfer ~/Downloads/scion-ietf126-as<N>.conf scion:/home/ubuntu/
multipass transfer ~/Downloads/scion-testbed-ca.pem scion:/home/ubuntu/
```

(WSL2/native: the files just need to be readable from your shell.)

After claiming, the page shows a tab per joinable AS — each with its own
**bootstrap server URL** (`http://10.20.3.<N>:8041`). Pick the AS you want
to be an endhost in; you'll type its URL into the script in a minute.

## 3. Run the bootstrap script

Inside the VM/shell — download **then** run (it's interactive; don't pipe
it into bash):

```
curl -fsSLO https://codeberg.org/lschulz/scitra-bootstrapper/raw/branch/scion-in-a-box/bootstrap-scitra.bash
bash bootstrap-scitra.bash
```

The script installs the SCION daemon + tools and scitra-tun from the
author's apt repository, then walks you through setup (expect a couple of
ordinary apt "Do you want to continue?" prompts too). Answer sheet:

| Prompt | Answer |
|---|---|
| Set up Wireguard? | **y**, then the path to your `.conf` |
| Select a bootstrap server | **1** (enter manually) → `http://10.20.3.<N>:8041` from your AS tab |
| Proceed over HTTP anyway? | **y** — this URL only exists inside your tunnel |
| Install this configuration? | **y** (after it shows you the TRC + topology) |
| update-scion script / timer | **y** / **y** |
| Enable STUN and NAT traversal? | **n** — the tunnel means no NAT on the SCION path, even from inside a NAT'd VM |
| Host address / interface correct? | **y** (your `10.20.5.x` on the WG interface) |
| TUN interface address | just press Enter (uses your SCION-mapped `fc00…` address) |
| Configure SCION DNS? | **y** — `*.scion` names then resolve *over SCION* |

The script verifies each stage itself (paths, `scion ping`, scitra
translation, DNS) and is safe to re-run if anything is interrupted.

## 4. Try it

```
scion address                         # who am I? 1-<N>,10.20.5.x
scion showpaths 1-150                 # real paths through the testbed
scion ping 1-150,10.20.3.215          # SCMP over SCION
ping -6 welcome.scion                 # ordinary IPv6 ping — riding SCION via scitra
curl --cacert scion-testbed-ca.pem https://welcome.scion/
```

Watch the dashboard map (`/`) light up your path while a ping runs. The
`fc00…` addresses scitra gives you encode ISD + AS + host — see `faq.md`.

## 5. Caveats

- Keep tunnelled SCION payloads under **~1200 bytes** (WG + SCION headers
  eat into the path MTU; oversized packets fail cleanly, they don't
  corrupt).
- The testbed CA is a throwaway — use `--cacert`, never install it
  system-wide, and don't trust it outside the testbed.
- `multipass delete scion --purge` removes the VM when you're done; the
  whole testbed is torn down after the event anyway.

## Appendix — Apple Silicon (arm64): build from source

The apt repo has no arm64 packages, but upstream SCION builds fine in an
arm64 VM (~5–10 min; needs Go 1.23+, a C compiler, internet):

```
git clone https://github.com/scionproto/scion && cd scion
git checkout v0.15.0
CGO_ENABLED=1 go build -o bin/ ./daemon/cmd/daemon ./scion/cmd/scion ./dispatcher/cmd/dispatcher
```

`CGO_ENABLED=1` matters: v0.15.0 uses the cgo sqlite driver, and binaries
built without it (including the official release downloads) panic on
startup (`go-sqlite3 … is a stub`).

Then configure by hand instead of the script: bring the tunnel up
(`sudo wg-quick up ./scion-ietf126-as<N>.conf`), download your AS bundle
from its tab on this page (`scion-endhost-AS<N>.tar.gz`), untar it into an
empty directory and run `bin/daemon --config sd.toml` from in there.
`bin/scion showpaths 1-150 --sciond 127.0.0.1:30255` should then work.
scitra-tun can also be built from source
(`github.com/lschulz/scion-cpp`) if you want the `fc00…`/DNS magic —
optional. *(This appendix is untested on real Apple-Silicon hardware —
flag us down at the booth if you hit trouble.)*

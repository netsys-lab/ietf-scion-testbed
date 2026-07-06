# Laptop — macOS

Bring your own Mac onto the SCION testbed as a real endhost in one of ASes
1-158..1-161. Same source-build path as Linux; no scitra (Linux only, see
the note at the end).

1. **WireGuard**: `brew install wireguard-tools` (needs Homebrew). Use the
   `wg-quick` CLI below — the WireGuard.app from the App Store also works if
   you prefer importing the conf there instead of steps 2/3.

2. **Claim your conf** on this page (`/join`) — pick an AS, enter the booth
   code. Save the download as `scion-ietf126-as<N>.conf`, then:

   ```
   sudo wg-quick up ./scion-ietf126-as<N>.conf
   ```

   Check the tunnel is up: `ping 10.20.3.1` should answer.

3. **Build the SCION tools from source, upstream `v0.15.0`.** We proved on
   2026-07-06 that upstream scionproto at tag `v0.15.0`, built with
   `CGO_ENABLED=1`, interoperates with this testbed as an endhost (0% loss
   ping against the fork). Do **not** use the official release binaries —
   see the note below. Build from source instead (needs Go 1.23+ — Go's
   toolchain directive will fetch a newer Go automatically on first build,
   so you need internet access at build time — and Xcode command line
   tools for the cgo toolchain: `xcode-select --install` if you don't
   already have them):

   ```
   git clone https://github.com/scionproto/scion
   cd scion
   git checkout v0.15.0
   CGO_ENABLED=1 go build -o bin/ ./daemon/cmd/daemon ./scion/cmd/scion ./dispatcher/cmd/dispatcher
   ```

   The build produces `bin/daemon`, `bin/scion`, `bin/dispatcher`.

   **Do not use the official release binaries.** `v0.15.0` reverted to the
   mattn/go-sqlite3 driver, which requires cgo — but the official release
   binaries are built `CGO_ENABLED=0`, so their `scion-daemon` panics on
   startup (`Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires
   cgo to work. This is a stub`) before it can even open its trust/path DB.
   There is no config workaround; building from source with
   `CGO_ENABLED=1` as above (the macOS system toolchain handles the cgo
   dependency fine) is the supported path.

4. **Bundle**: download your AS bundle from this page (or
   `/api/join/bundle/<N>`, filename `scion-endhost-AS<N>.tar.gz`), untar it
   into an empty directory:

   ```
   mkdir -p ~/scion-kit && cd ~/scion-kit
   tar xzf ~/Downloads/scion-endhost-AS<N>.tar.gz
   /path/to/scion/bin/daemon --config sd.toml
   ```

5. **Go**: with the daemon running in that terminal, in another one:

   ```
   ./bin/scion showpaths 1-160 --sciond 127.0.0.1:30255
   ./bin/scion ping 1-161,10.20.3.213 --sciond 127.0.0.1:30255
   ```

   Watch the dashboard map (`/`) light up your path while the ping runs.
   See `faq.md` for the payload-size ceiling before you try anything bigger
   than a ping.

6. **Be pingable** (optional): run `./bin/dispatcher` alongside the daemon
   so other attendees' `scion ping` can reach your `10.20.5.x` tunnel
   address.

**No scitra on macOS.** `scitra-tun` (the tool that lets plain `ping -6
fc00:...` ride SCION transparently) is Linux-only — it needs a Linux TUN
device and SCMP-echo integration that hasn't been ported. On macOS, stick
to the `scion` CLI (`showpaths`/`ping`/`traceroute`) for the demo, or visit
one of the browser playground terminals on this page for the fc00 demo.

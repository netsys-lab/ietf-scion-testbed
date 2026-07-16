# Laptop — Linux

Bring your own laptop/VM onto the SCION testbed as a real endhost in one of
ASes 1-152, 1-155, 1-158, 1-161.

1. **WireGuard**: `sudo apt install wireguard`

2. **Claim your conf** on this page (`/join`) — enter the booth code. There's
   no AS picker at claim time: one conf tunnels the whole testbed, and the
   page names the download `scion-ietf126-as<N>.conf` for you. Then:

   ```
   sudo wg-quick up ./scion-ietf126-as<N>.conf
   ```

   The conf sets `DNS = 10.20.3.216` (the testbed's SCION-aware resolver,
   which also resolves normal names via Quad9). Delete the `DNS =` line
   before bringing the tunnel up if you'd rather keep your own resolver.

   Check the tunnel is up: `ping 10.20.3.1` should answer (that's the
   testbed's management gateway, reachable once the tunnel is up).

   After claiming, the page shows a tab per joinable AS (1-152, 1-155, 1-158,
   1-161) — each has its own downloadable endhost bundle, scitra fc00
   identity, and bootstrap-server link. Pick whichever AS(es) you want to be
   an endhost in for steps 4 and 7 below; you can set up in more than one.

3. **Build the SCION tools from source, upstream `v0.15.0`.** We proved on
   2026-07-06 that upstream scionproto at tag `v0.15.0`, built with
   `CGO_ENABLED=1`, interoperates with this testbed as an endhost (0% loss
   ping against the fork). Do **not** use the official release binaries —
   see the note below. Build from source instead (needs a C compiler and Go
   1.23+; Go's toolchain directive will fetch a newer Go automatically on
   first build, so you need internet access at build time):

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
   `CGO_ENABLED=1` as above is the supported path.

4. **Bundle**: download your AS bundle from the tab you picked on this page
   (or `/api/join/bundle/<N>` directly, filename `scion-endhost-AS<N>.tar.gz`),
   untar it into an empty directory:

   ```
   mkdir ~/scion-kit && cd ~/scion-kit
   tar xzf ~/Downloads/scion-endhost-AS<N>.tar.gz
   /path/to/scion/bin/daemon --config sd.toml
   ```

   (run the `daemon` binary you built in step 3 from inside the unpacked
   bundle directory — `sd.toml` uses relative paths, so `cd` there first.)

   **Alternative to hand-unpacking**: if your `sciond` build supports HTTP
   bootstrap discovery, point it at `http://10.20.3.<AS>:8041` (the bootstrap
   URL shown on your AS's tab) instead of the tar/untar dance above — it
   serves the same `topology.json` + TRC that's in the bundle.

5. **Go**: with the daemon running in that terminal, in another one:

   ```
   ./bin/scion showpaths 1-160 --sciond 127.0.0.1:30255
   ./bin/scion ping 1-161,10.20.3.213 --sciond 127.0.0.1:30255
   ```

   Watch the dashboard map (this page, `/`) light up your path while the
   ping runs. See `faq.md` for the payload-size ceiling before you try
   anything bigger than a ping.

6. **Be pingable** (optional): run `./bin/dispatcher` alongside the daemon
   so other attendees' `scion ping` can reach your `10.20.5.x` tunnel
   address.

7. **scitra — plain IPv6 over SCION** (optional, Linux only): build
   `scitra-tun` from `github.com/lschulz/scion-cpp` (see that repo's
   README for build deps), then run it pointed at your daemon, e.g.:

   ```
   sudo ./scitra-tun --scmp -d 127.0.0.1:30255
   ```

   Once it's up, plain `ping -6 fc00:...` (use the `fc00...` identity shown
   on your AS's tab on this page) rides SCION underneath — no SCION tooling
   involved on the sending side, just ordinary IPv6.

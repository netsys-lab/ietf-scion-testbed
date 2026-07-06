# Laptop — Linux

Bring your own laptop onto the SCION testbed as a real endhost in one of
ASes 1-158..1-161. Takes about 10 minutes; the SCION build is the slow part.

1. **WireGuard**: `sudo apt install wireguard` (Debian/Ubuntu — use your
   distro's package otherwise, e.g. `sudo dnf install wireguard-tools`).

2. **Claim your conf** on this page (`/join`) — pick an AS, enter the booth
   code. Save the download as `scion-ietf126-as<N>.conf` (the page names it
   for you), then:

   ```
   sudo wg-quick up ./scion-ietf126-as<N>.conf
   ```

   Check the tunnel is up: `ping 10.20.3.1` should answer (that's the
   testbed's management gateway, reachable once the tunnel is up).

3. **Build the SCION tools from source.** We tested the latest upstream
   release binaries against this testbed and they don't work: every
   official `scion-daemon` package is built with `CGO_ENABLED=0`, and its
   sqlite driver requires cgo — the daemon panics on startup
   (`Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to
   work. This is a stub`) before it can even open its trust/path DB. There
   is no config workaround. So: **build from source, pinned to the commit
   this testbed's fork is based on** (needs Go 1.22+):

   ```
   git clone https://github.com/scionproto/scion
   cd scion
   git checkout c4d1b5bd8
   CGO_ENABLED=1 go build -o bin/ ./scion/cmd/scion ./daemon/cmd/daemon ./dispatcher/cmd/dispatcher
   ```

   `CGO_ENABLED=1` is required — the daemon needs cgo for its sqlite trust/
   path databases; this is exactly the flag upstream's release builds get
   wrong for our purposes. The build produces `bin/scion`, `bin/daemon`,
   `bin/dispatcher`.

4. **Bundle**: download your AS bundle from this page (or
   `/api/join/bundle/<N>`, filename `scion-endhost-AS<N>.tar.gz`), untar it
   into an empty directory:

   ```
   mkdir ~/scion-kit && cd ~/scion-kit
   tar xzf ~/Downloads/scion-endhost-AS<N>.tar.gz
   /path/to/scion/bin/daemon --config sd.toml
   ```

   (run the `daemon` binary you built in step 3 from inside the unpacked
   bundle directory — `sd.toml` uses relative paths, so `cd` there first.)

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
   on this page for your claimed conf) rides SCION underneath — no SCION
   tooling involved on the sending side, just ordinary IPv6.

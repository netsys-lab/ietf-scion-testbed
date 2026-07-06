# Try it now — browser playground

Zero install: a live SCION endhost shell right in your browser, running
inside one of ASes 1-158..1-161 on the real testbed. Good if you don't want
to install anything, or just want a 2-minute taste before setting up your
own laptop (see `laptop-linux.md` / `laptop-macos.md`).

1. **Open a terminal.** On this page, pick one of the "AS 1-158..161
   terminal" cards. You'll land on a booth-code-gated web terminal
   (`/play/<AS>/`) — enter the booth code when prompted.

2. **Look around.** You're logged into a sandboxed shell inside that AS's
   endhost. The MOTD lists a few things to try; the short version:

   ```
   scion showpaths 1-159 --extended     # paths to another AS
   scion ping 1-160,10.20.3.212         # ping an endhost in AS 1-160
   scion traceroute 1-161,10.20.3.213
   ```

   Run any of these and switch over to the dashboard map (`/`) in another
   tab — you'll see the path you just used light up live.

3. **The fc00 demo (plain IPv6 over SCION).** Every playground endhost also
   runs `scitra-tun`, which transparently maps an `fc00::/8` IPv6 address
   onto a SCION endhost identity. That means an ordinary `ping -6` — no
   SCION tooling, no special flags — actually rides SCION underneath. From
   an AS-158/159/160 shell:

   ```
   ping -6 fc00:1000:a100::ffff:10.20.3.213
   ```

   (that address is AS 1-161's playground endhost; from the AS-161 shell
   itself, ping AS 1-158's instead: `fc00:1000:9e00::ffff:10.20.3.210`).
   The `fc00...` prefix encodes the target ISD + AS number — see `faq.md`
   for why it looks different depending on which AS you're pinging from or
   into.

4. **Session lifetime.** Your shell is sandboxed and resets when you
   disconnect — nothing you do there persists or affects other attendees.
   Reconnect any time by clicking the card again.

5. **Want your own endhost instead?** The "Join with your laptop"
   section on this page walks you through claiming a WireGuard conf and
   running real SCION tools locally — same testbed, your own machine.

# Try it now — browser playground

Zero install: a live SCION endhost shell right in your browser, running
inside one of ASes 1-152, 1-155, 1-158, 1-161 on the real testbed. Good if
you don't want to install anything, or just want a 2-minute taste before
setting up your own laptop (see `laptop-linux.md`).

1. **Open a terminal.** On this page, pick one of the "AS 1-152/1-155/1-158/1-161
   terminal" cards. You'll land on a booth-code-gated web terminal
   (`/play/<AS>/`) — enter the booth code when prompted.

2. **Look around.** You're logged into a sandboxed shell inside that AS's
   endhost. The MOTD lists a few things to try; the short version:

   ```
   scion showpaths 1-150 --extended     # paths to a core AS
   scion ping 1-150,10.20.3.150         # ping a border router in AS 1-150
   scion traceroute 1-153,10.20.3.153
   ```

   Run any of these and switch over to the dashboard map (`/`) in another
   tab — you'll see the path you just used light up live.

3. **The fc00 demo (plain IPv6 over SCION).** Every playground endhost also
   runs `scitra-tun`, which transparently maps an `fc00::/8` IPv6 address
   onto a SCION endhost identity. That means an ordinary `ping -6` — no
   SCION tooling, no special flags — actually rides SCION underneath. The
   `fc00...` prefix encodes the target ISD + AS number, so the address you
   ping looks different depending on which AS you're in and which AS you're
   pinging into — see `faq.md` for the full explanation and worked example.

4. **Session lifetime.** Your shell is sandboxed and resets when you
   disconnect — nothing you do there persists or affects other attendees.
   Reconnect any time by clicking the card again.

5. **Want your own endhost instead?** The "Join with your laptop"
   section on this page walks you through claiming a WireGuard conf and
   running real SCION tools locally — same testbed, your own machine.

IETF 126 Vienna Hackathon SCION Testbed
=======================================

This repo delivers the testbed's live dashboard (the `fabricd` backend plus
its web frontend), the `scion-linkd` link-shaping daemon running in every AS
container, and the beacon-metadata sync that keeps both in step with the
topology. To build, deploy, and operate: see [DEPLOY.md](DEPLOY.md).

```
 Management Network            IETF IPv4+IPv6 Network
     10.20.3.0/24
                    +-------+
               +----| AS150 |----+
               |    +-------+    |
               |       ...       |
               |    +-------+    |
               +----| AS161 |----+
+--------+     |    +-------+    |
|Kea DHCP|-----+                 |
+--------+     |                 |
            +-----+         +---------+
            |vmbr0|         | pubnet  |
            +-----+         +---------+
               |             |   |   |
              ETH0         ETH1 ETH2 ETH3
```

### Open Questions ###
How to configure internal addresses of the border routers so they are exposed to
pubnet?

### Docs ###
- Kea DHCP Server
    - https://kea.readthedocs.io/en/stable/index.html

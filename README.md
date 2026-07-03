IETF 126 Vienna Hackathon SCION Testbed
=======================================

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

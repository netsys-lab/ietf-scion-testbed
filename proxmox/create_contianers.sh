#!/bin/bash

set -e

# Resolve paths relative to this script, not the caller's CWD, so the SSH
# public-key file is found no matter where the script is invoked from.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Template: override with TEMPLATE=... ; default is the Ubuntu 24.04 LTS
# standard LXC template (glibc 2.39 — the SCION reference platform).
TEMPLATE="${TEMPLATE:-local:vztmpl/ubuntu-24.04-standard_24.04-2_amd64.tar.zst}"

# CPU/memory tiers (spec docs/superpowers/specs/2026-07-07-cpu-weights-design.md):
# dataplane (AS) > dashboard > wg-hub > playground. cpuunits maps to cgroup-v2
# cpu.weight (PVE CT default 100): under contention a border router gets 20x
# a playground container's share; idle boxes stay work-conserving (no hard
# cpulimit). Keep DEPLOY.md's CT214 (svc endhost) stanza in sync with
# PLAY_OPTS. Memory values match the live fleet (the old blanket
# "--memory 2048" was drift).
# --rootfs on local-lvm (lvmthin): the only storage here that supports container
# rootdir. Without it pct defaults to 'local' (dir storage) and fails with
# "storage 'local' does not support container directories". 8 GiB thin cap.
# --features nesting=1: Ubuntu 24.04 ships systemd 255, which warns/misbehaves
# inside an LXC container without nesting ("Systemd 255 detected. You may need
# to enable nesting."). Debian 12's systemd 252 didn't need it.
COMMON="--swap 512 --ssh-public-keys $SCRIPT_DIR/public_keys --rootfs local-lvm:8 --features nesting=1"
AS_OPTS="--cores 2 --memory 1024 --cpuunits 1000 $COMMON"
DASH_OPTS="--cores 2 --memory 1024 --cpuunits 300 $COMMON"
HUB_OPTS="--cores 2 --memory 1024 --cpuunits 200 $COMMON"
PLAY_OPTS="--cores 1 --memory 1024 --cpuunits 50 $COMMON"

# CT100 (Kea DHCP server) is NOT used on the reconstructed ietf-proxmox node:
# containers have static IPs and the HOST holds 10.20.3.1 on the mgmt bridge.
# Creating this container there would IP-conflict with the mgmt gateway and
# take down the whole management plane. Kept for reference only.
# pct create 100 $TEMPLATE $AS_OPTS --description "DHCP Server" \
#     --net0 name=eth0,bridge=vmbr0,ip=10.20.3.1/24,ip6=auto

pct create 150 $TEMPLATE $AS_OPTS --description "AS150" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:01,ip=10.20.3.150/24,gw=10.20.3.1 \
    --net2 name=sci1,bridge=scion1,ip6=fd00:fade:1::150/64 \
    --net3 name=sci2,bridge=scion2,ip6=fd00:fade:2::150/64 \
    --net4 name=sci3,bridge=scion3,ip6=fd00:fade:3::150/64 \
    --net5 name=sci7,bridge=scion7,ip6=fd00:fade:7::150/64

pct create 151 $TEMPLATE $AS_OPTS --description "AS151" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:02,ip=10.20.3.151/24,gw=10.20.3.1 \
    --net2 name=sci1,bridge=scion1,ip6=fd00:fade:1::151/64 \
    --net3 name=sci4,bridge=scion4,ip6=fd00:fade:4::151/64 \
    --net4 name=sci8,bridge=scion8,ip6=fd00:fade:8::151/64 \
    --net5 name=sci9,bridge=scion9,ip6=fd00:fade:9::151/64 \
    --net6 name=sci5,bridge=scion5,ip6=fd00:fade:5::151/64

pct create 152 $TEMPLATE $AS_OPTS --description "AS152" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:03,ip=10.20.3.152/24,gw=10.20.3.1 \
    --net2 name=sci4,bridge=scion4,ip6=fd00:fade:4::152/64 \
    --net3 name=sci2,bridge=scion2,ip6=fd00:fade:2::152/64 \
    --net4 name=sci6,bridge=scion6,ip6=fd00:fade:6::152/64 \
    --net5 name=sciA,bridge=scionA,ip6=fd00:fade:a::152/64 \
    --net6 name=sciB,bridge=scionB,ip6=fd00:fade:b::152/64 \
    --net7 name=sciC,bridge=scionC,ip6=fd00:fade:c::152/64

pct create 153 $TEMPLATE $AS_OPTS --description "AS153" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:04,ip=10.20.3.153/24,gw=10.20.3.1 \
    --net2 name=sci3,bridge=scion3,ip6=fd00:fade:3::153/64 \
    --net3 name=sci5,bridge=scion5,ip6=fd00:fade:5::153/64 \
    --net4 name=sci6,bridge=scion6,ip6=fd00:fade:6::153/64 \
    --net5 name=sciD,bridge=scionD,ip6=fd00:fade:d::153/64 \
    --net6 name=sciE,bridge=scionE,ip6=fd00:fade:e::153/64

pct create 154 $TEMPLATE $AS_OPTS --description "AS154" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:05,ip=10.20.3.154/24,gw=10.20.3.1 \
    --net2 name=sci7,bridge=scion7,ip6=fd00:fade:7::154/64 \
    --net3 name=sci8,bridge=scion8,ip6=fd00:fade:8::154/64 \
    --net4 name=sciF,bridge=scionF,ip6=fd00:fade:f::154/64 \
    --net5 name=sci10,bridge=scion10,ip6=fd00:fade:10::154/64

pct create 155 $TEMPLATE $AS_OPTS --description "AS155" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:06,ip=10.20.3.155/24,gw=10.20.3.1 \
    --net2 name=sci9,bridge=scion9,ip6=fd00:fade:9::155/64 \
    --net3 name=sciA,bridge=scionA,ip6=fd00:fade:a::155/64 \
    --net4 name=sci10,bridge=scion10,ip6=fd00:fade:10::155/64 \
    --net5 name=sci11,bridge=scion11,ip6=fd00:fade:11::155/64 \
    --net6 name=sci12,bridge=scion12,ip6=fd00:fade:12::155/64 \
    --net7 name=sci14,bridge=scion14,ip6=fd00:fade:14::155/64 \
    --net8 name=sci15,bridge=scion15,ip6=fd00:fade:15::155/64 \
    --net9 name=sci16,bridge=scion16,ip6=fd00:fade:16::155/64

pct create 156 $TEMPLATE $AS_OPTS --description "AS156" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:07,ip=10.20.3.156/24,gw=10.20.3.1 \
    --net2 name=sciB,bridge=scionB,ip6=fd00:fade:b::156/64 \
    --net3 name=sciD,bridge=scionD,ip6=fd00:fade:d::156/64 \
    --net4 name=sci12,bridge=scion12,ip6=fd00:fade:12::156/64 \
    --net5 name=sci17,bridge=scion17,ip6=fd00:fade:17::156/64

pct create 157 $TEMPLATE $AS_OPTS --description "AS157" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:08,ip=10.20.3.157/24,gw=10.20.3.1 \
    --net2 name=sciC,bridge=scionC,ip6=fd00:fade:c::157/64 \
    --net3 name=sciE,bridge=scionE,ip6=fd00:fade:e::157/64 \
    --net4 name=sci18,bridge=scion18,ip6=fd00:fade:18::157/64

pct create 158 $TEMPLATE $AS_OPTS --description "AS158" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:09,ip=10.20.3.158/24,gw=10.20.3.1 \
    --net2 name=sciF,bridge=scionF,ip6=fd00:fade:f::158/64 \
    --net3 name=sci11,bridge=scion11,ip6=fd00:fade:11::158/64 \
    --net4 name=sci13,bridge=scion13,ip6=fd00:fade:13::158/64

pct create 159 $TEMPLATE $AS_OPTS --description "AS159" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:0A,ip=10.20.3.159/24,gw=10.20.3.1 \
    --net2 name=sci13,bridge=scion13,ip6=fd00:fade:13::159/64 \
    --net3 name=sci14,bridge=scion14,ip6=fd00:fade:14::159/64

pct create 160 $TEMPLATE $AS_OPTS --description "AS160" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:0B,ip=10.20.3.160/24,gw=10.20.3.1 \
    --net2 name=sci15,bridge=scion15,ip6=fd00:fade:15::160/64

pct create 161 $TEMPLATE $AS_OPTS --description "AS161" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:0C,ip=10.20.3.161/24,gw=10.20.3.1 \
    --net2 name=sci16,bridge=scion16,ip6=fd00:fade:16::161/64 \
    --net3 name=sci17,bridge=scion17,ip6=fd00:fade:17::161/64 \
    --net4 name=sci18,bridge=scion18,ip6=fd00:fade:18::161/64

pct create 200 $TEMPLATE $DASH_OPTS --description "Dashboard" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:C8,ip=10.20.3.200/24 \
    --net1 name=eth1,bridge=vmbr0,ip=dhcp,ip6=auto

pct create 201 $TEMPLATE $HUB_OPTS --unprivileged 1 --description "wg-hub" \
    --net0 name=eth0,bridge=mgmt,ip=10.20.3.201/24 \
    --net1 name=eth1,bridge=vmbr0,ip=dhcp,ip6=auto

# --- Attendee playground endhosts (Tier 1): one per hospitality AS ---
pct create 210 $TEMPLATE $PLAY_OPTS --description "play-158" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:D2,ip=10.20.3.210/24,gw=10.20.3.1

pct create 211 $TEMPLATE $PLAY_OPTS --description "play-159" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:D3,ip=10.20.3.211/24,gw=10.20.3.1

pct create 212 $TEMPLATE $PLAY_OPTS --description "play-160" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:D4,ip=10.20.3.212/24,gw=10.20.3.1

pct create 213 $TEMPLATE $PLAY_OPTS --description "play-161" \
    --net0 name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:D5,ip=10.20.3.213/24,gw=10.20.3.1

# --- Headless service endhost: svc-151 (scitra --scmp + fork sciond) ---
# PRIVILEGED (not a guest-facing shell): it needs full net capabilities
# (raw sockets for ping/scmp, etc.). nesting=1 for systemd 255.
# Venue leg (eth1 on vmbr0, DHCP v4 + SLAAC v6): svc-151 hosts services (DNS)
# reachable from the network by regular-IP clients, not only over SCION. The
# venue net is globally routable, so this leg is firewalled by ufw (deny
# incoming; only mgmt/eth0 trusted) — see deploy_svc_endhost.yaml.
pct create 214 $TEMPLATE --cores 1 --memory 2048 --swap 512 --cpuunits 50 \
    --rootfs local-lvm:4 --ssh-public-keys $SCRIPT_DIR/public_keys \
    --unprivileged 0 --features nesting=1 --onboot 1 --description "svc-151" \
    --net0 name=eth0,bridge=mgmt,ip=10.20.3.214/24,gw=10.20.3.1 \
    --net1 name=eth1,bridge=vmbr0,ip=dhcp,ip6=auto

# scitra-tun (playground 210-213 + svc-151 214) needs /dev/net/tun, which is a
# host-level LXC passthrough, NOT a pct flag. Load the module (persist it) and
# append the two raw lxc.* lines to each container's config, then reboot so the
# bind mount takes effect. Idempotent.
modprobe tun 2>/dev/null || true
grep -qx tun /etc/modules-load.d/tun.conf 2>/dev/null || echo tun > /etc/modules-load.d/tun.conf
for id in 210 211 212 213 214; do
    conf="/etc/pve/lxc/$id.conf"
    if [ -f "$conf" ] && ! grep -q "dev/net/tun" "$conf"; then
        printf 'lxc.cgroup2.devices.allow: c 10:200 rwm\nlxc.mount.entry: /dev/net/tun dev/net/tun none bind,create=file\n' >> "$conf"
        pct reboot "$id" 2>/dev/null || true
    fi
done

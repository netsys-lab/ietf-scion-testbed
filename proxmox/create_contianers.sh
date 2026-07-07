#!/bin/bash

set -e

# Template: override with TEMPLATE=... ; default matches DEPLOY.md's CT214 stanza.
TEMPLATE="${TEMPLATE:-local:vztmpl/debian-12-standard_12.12-1_amd64.tar.zst}"

# CPU/memory tiers (spec docs/superpowers/specs/2026-07-07-cpu-weights-design.md):
# dataplane (AS) > dashboard > wg-hub > playground. cpuunits maps to cgroup-v2
# cpu.weight (PVE CT default 100): under contention a border router gets 20x
# a playground container's share; idle boxes stay work-conserving (no hard
# cpulimit). Keep DEPLOY.md's CT214 (svc endhost) stanza in sync with
# PLAY_OPTS. Memory values match the live fleet (the old blanket
# "--memory 2048" was drift).
COMMON="--swap 512 --ssh-public-keys ./public_keys"
AS_OPTS="--cores 2 --memory 1024 --cpuunits 1000 $COMMON"
DASH_OPTS="--cores 2 --memory 1024 --cpuunits 300 $COMMON"
HUB_OPTS="--cores 2 --memory 1024 --cpuunits 200 $COMMON"
PLAY_OPTS="--cores 1 --memory 512 --cpuunits 50 $COMMON"

# CT100 (Kea DHCP server) is NOT used on the reconstructed ietf-proxmox node:
# containers have static IPs and the HOST holds 10.20.3.1 on the mgmt bridge.
# Creating this container there would IP-conflict with the mgmt gateway and
# take down the whole management plane. Kept for reference only.
# pct create 100 $TEMPLATE $AS_OPTS --description "DHCP Server" \
#     --net0,name=eth0,bridge=vmbr0,ip=10.20.3.1/24,ip6=auto

pct create 150 $TEMPLATE $AS_OPTS --description "AS150" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:01,ip=10.20.3.150/24,gw=10.20.3.1 \
    --net2,name=sci1,bridge=scion1,ip6=fd00:fade:1::150/64 \
    --net3,name=sci2,bridge=scion2,ip6=fd00:fade:2::150/64 \
    --net4,name=sci3,bridge=scion3,ip6=fd00:fade:3::150/64 \
    --net5,name=sci7,bridge=scion7,ip6=fd00:fade:7::150/64

pct create 151 $TEMPLATE $AS_OPTS --description "AS151" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:02,ip=10.20.3.151/24,gw=10.20.3.1 \
    --net2,name=sci1,bridge=scion1,ip6=fd00:fade:1::151/64 \
    --net3,name=sci4,bridge=scion4,ip6=fd00:fade:4::151/64 \
    --net4,name=sci8,bridge=scion8,ip6=fd00:fade:8::151/64 \
    --net5,name=sci9,bridge=scion9,ip6=fd00:fade:9::151/64 \
    --net6,name=sci5,bridge=scion5,ip6=fd00:fade:5::151/64

pct create 152 $TEMPLATE $AS_OPTS --description "AS152" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:03,ip=10.20.3.152/24,gw=10.20.3.1 \
    --net2,name=sci4,bridge=scion4,ip6=fd00:fade:4::152/64 \
    --net3,name=sci2,bridge=scion2,ip6=fd00:fade:2::152/64 \
    --net4,name=sci6,bridge=scion6,ip6=fd00:fade:6::152/64 \
    --net5,name=sciA,bridge=scionA,ip6=fd00:fade:a::152/64 \
    --net6,name=sciB,bridge=scionB,ip6=fd00:fade:b::152/64 \
    --net7,name=sciC,bridge=scionC,ip6=fd00:fade:c::152/64

pct create 153 $TEMPLATE $AS_OPTS --description "AS153" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:04,ip=10.20.3.153/24,gw=10.20.3.1 \
    --net2,name=sci3,bridge=scion3,ip6=fd00:fade:3::153/64 \
    --net3,name=sci5,bridge=scion5,ip6=fd00:fade:5::153/64 \
    --net4,name=sci6,bridge=scion6,ip6=fd00:fade:6::153/64 \
    --net5,name=sciD,bridge=scionD,ip6=fd00:fade:d::153/64 \
    --net6,name=sciE,bridge=scionE,ip6=fd00:fade:e::153/64

pct create 154 $TEMPLATE $AS_OPTS --description "AS154" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:05,ip=10.20.3.154/24,gw=10.20.3.1 \
    --net2,name=sci7,bridge=scion7,ip6=fd00:fade:7::154/64 \
    --net3,name=sci8,bridge=scion8,ip6=fd00:fade:8::154/64 \
    --net4,name=sciF,bridge=scionF,ip6=fd00:fade:f::154/64 \
    --net5,name=sci10,bridge=scion10,ip6=fd00:fade:10::154/64

pct create 155 $TEMPLATE $AS_OPTS --description "AS155" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:06,ip=10.20.3.155/24,gw=10.20.3.1 \
    --net2,name=sci9,bridge=scion9,ip6=fd00:fade:9::155/64 \
    --net3,name=sciA,bridge=scionA,ip6=fd00:fade:a::155/64 \
    --net4,name=sci10,bridge=scion10,ip6=fd00:fade:10::155/64 \
    --net5,name=sci11,bridge=scion11,ip6=fd00:fade:11::155/64 \
    --net6,name=sci12,bridge=scion12,ip6=fd00:fade:12::155/64 \
    --net7,name=sci14,bridge=scion14,ip6=fd00:fade:14::155/64 \
    --net8,name=sci15,bridge=scion15,ip6=fd00:fade:15::155/64 \
    --net9,name=sci16,bridge=scion16,ip6=fd00:fade:16::155/64

pct create 156 $TEMPLATE $AS_OPTS --description "AS156" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:07,ip=10.20.3.156/24,gw=10.20.3.1 \
    --net2,name=sciB,bridge=scionB,ip6=fd00:fade:b::156/64 \
    --net3,name=sciD,bridge=scionD,ip6=fd00:fade:d::156/64 \
    --net4,name=sci12,bridge=scion12,ip6=fd00:fade:12::156/64 \
    --net5,name=sci17,bridge=scion17,ip6=fd00:fade:17::156/64

pct create 157 $TEMPLATE $AS_OPTS --description "AS157" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:08,ip=10.20.3.157/24,gw=10.20.3.1 \
    --net2,name=sciC,bridge=scionC,ip6=fd00:fade:c::157/64 \
    --net3,name=sciE,bridge=scionE,ip6=fd00:fade:e::157/64 \
    --net4,name=sci18,bridge=scion18,ip6=fd00:fade:18::157/64

pct create 158 $TEMPLATE $AS_OPTS --description "AS158" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:09,ip=10.20.3.158/24,gw=10.20.3.1 \
    --net2,name=sciF,bridge=scionF,ip6=fd00:fade:f::158/64 \
    --net3,name=sci11,bridge=scion11,ip6=fd00:fade:11::158/64 \
    --net4,name=sci13,bridge=scion13,ip6=fd00:fade:13::158/64

pct create 159 $TEMPLATE $AS_OPTS --description "AS159" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:0A,ip=10.20.3.159/24,gw=10.20.3.1 \
    --net2,name=sci13,bridge=scion13,ip6=fd00:fade:13::159/64 \
    --net3,name=sci14,bridge=scion14,ip6=fd00:fade:14::159/64

pct create 160 $TEMPLATE $AS_OPTS --description "AS160" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:0B,ip=10.20.3.160/24,gw=10.20.3.1 \
    --net2,name=sci15,bridge=scion15,ip6=fd00:fade:15::160/64

pct create 161 $TEMPLATE $AS_OPTS --description "AS161" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:0C,ip=10.20.3.161/24,gw=10.20.3.1 \
    --net2,name=sci16,bridge=scion16,ip6=fd00:fade:16::161/64 \
    --net3,name=sci17,bridge=scion17,ip6=fd00:fade:17::161/64 \
    --net4,name=sci18,bridge=scion18,ip6=fd00:fade:18::161/64

pct create 200 $TEMPLATE $DASH_OPTS --description "Dashboard" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:C8,ip=10.20.3.200/24 \
    --net1,name=eth1,bridge=vmbr0,ip=dhcp,ip6=auto

pct create 201 $TEMPLATE $HUB_OPTS --unprivileged 1 --description "wg-hub" \
    --net0,name=eth0,bridge=mgmt,ip=10.20.3.201/24 \
    --net1,name=eth1,bridge=vmbr0,ip=dhcp,ip6=auto

# --- Attendee playground endhosts (Tier 1): one per hospitality AS ---
pct create 210 $TEMPLATE $PLAY_OPTS --description "play-158" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:D2,ip=10.20.3.210/24,gw=10.20.3.1

pct create 211 $TEMPLATE $PLAY_OPTS --description "play-159" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:D3,ip=10.20.3.211/24,gw=10.20.3.1

pct create 212 $TEMPLATE $PLAY_OPTS --description "play-160" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:D4,ip=10.20.3.212/24,gw=10.20.3.1

pct create 213 $TEMPLATE $PLAY_OPTS --description "play-161" \
    --net0,name=eth0,bridge=mgmt,hwaddr=00:00:00:00:00:D5,ip=10.20.3.213/24,gw=10.20.3.1

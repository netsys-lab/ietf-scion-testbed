#!/bin/bash

set -e

TEMPLATE:=TODO
OPTIONS=--cores 2 --memory 2048 --ssh-public-keys ./public_keys

pct create 100 $TEMPLATE $OPTIONS --description "DHCP Server" \
    --net0,name=eth0,bridge=vmbr0,ip=10.20.3.1/24,ip6=auto

pct create 150 $TEMPLATE $OPTIONS --description "AS150" \
    --net0,name=eth0,bridge=vmbr0,hwaddr=00:00:00:00:00:01,ip=dhcp,ip6=auto \
    --net1,name=eth1,bridge=pubnet,ip=dhcp,ip6=auto \
    --net2,name=sci1,bridge=scion1,ip6=fd00:fade:1::150/64 \
    --net3,name=sci2,bridge=scion2,ip6=fd00:fade:2::150/64 \
    --net4,name=sci3,bridge=scion3,ip6=fd00:fade:3::150/64 \
    --net5,name=sci7,bridge=scion7,ip6=fd00:fade:7::150/64

pct create 151 $TEMPLATE $OPTIONS --description "AS151" \
    --net0,name=eth0,bridge=vmbr0,hwaddr=00:00:00:00:00:02,ip=dhcp,ip6=auto \
    --net1,name=eth1,bridge=pubnet,ip=dhcp,ip6=auto \
    --net2,name=sci1,bridge=scion1,ip6=fd00:fade:1::151/64 \
    --net3,name=sci4,bridge=scion4,ip6=fd00:fade:4::151/64 \
    --net4,name=sci8,bridge=scion8,ip6=fd00:fade:8::151/64 \
    --net5,name=sci9,bridge=scion9,ip6=fd00:fade:9::151/64 \
    --net6,name=sci5,bridge=scion5,ip6=fd00:fade:5::151/64

pct create 152 $TEMPLATE $OPTIONS --description "AS152" \
    --net0,name=eth0,bridge=vmbr0,hwaddr=00:00:00:00:00:03,ip=dhcp,ip6=auto \
    --net1,name=eth1,bridge=pubnet,ip=dhcp,ip6=auto \
    --net2,name=sci4,bridge=scion4,ip6=fd00:fade:4::152/64 \
    --net3,name=sci2,bridge=scion2,ip6=fd00:fade:2::152/64 \
    --net4,name=sci6,bridge=scion6,ip6=fd00:fade:6::152/64 \
    --net5,name=sciA,bridge=scionA,ip6=fd00:fade:a::152/64 \
    --net6,name=sciB,bridge=scionB,ip6=fd00:fade:b::152/64 \
    --net7,name=sciC,bridge=scionC,ip6=fd00:fade:c::152/64

pct create 153 $TEMPLATE $OPTIONS --description "AS153" \
    --net0,name=eth0,bridge=vmbr0,hwaddr=00:00:00:00:00:04,ip=dhcp,ip6=auto \
    --net1,name=eth1,bridge=pubnet,ip=dhcp,ip6=auto \
    --net2,name=sci3,bridge=scion3,ip6=fd00:fade:3::153/64 \
    --net3,name=sci5,bridge=scion5,ip6=fd00:fade:5::153/64 \
    --net4,name=sci6,bridge=scion6,ip6=fd00:fade:6::153/64 \
    --net5,name=sciD,bridge=scionD,ip6=fd00:fade:d::153/64 \
    --net6,name=sciE,bridge=scionE,ip6=fd00:fade:e::153/64

pct create 154 $TEMPLATE $OPTIONS --description "AS154" \
    --net0,name=eth0,bridge=vmbr0,hwaddr=00:00:00:00:00:05,ip=dhcp,ip6=auto \
    --net1,name=eth1,bridge=pubnet,ip=dhcp,ip6=auto \
    --net2,name=sci7,bridge=scion7,ip6=fd00:fade:7::154/64 \
    --net3,name=sci8,bridge=scion8,ip6=fd00:fade:8::154/64 \
    --net4,name=sciF,bridge=scionF,ip6=fd00:fade:f::154/64 \
    --net5,name=sci10,bridge=scion10,ip6=fd00:fade:10::154/64

pct create 155 $TEMPLATE $OPTIONS --description "AS155" \
    --net0,name=eth0,bridge=vmbr0,hwaddr=00:00:00:00:00:06,ip=dhcp,ip6=auto \
    --net1,name=eth1,bridge=pubnet,ip=dhcp,ip6=auto \
    --net2,name=sci9,bridge=scion9,ip6=fd00:fade:9::155/64 \
    --net3,name=sciA,bridge=scionA,ip6=fd00:fade:a::155/64 \
    --net4,name=sci10,bridge=scion10,ip6=fd00:fade:10::155/64 \
    --net5,name=sci11,bridge=scion11,ip6=fd00:fade:11::155/64 \
    --net6,name=sci12,bridge=scion12,ip6=fd00:fade:12::155/64 \
    --net7,name=sci14,bridge=scion14,ip6=fd00:fade:14::155/64 \
    --net8,name=sci15,bridge=scion15,ip6=fd00:fade:15::155/64 \
    --net9,name=sci16,bridge=scion16,ip6=fd00:fade:16::155/64

pct create 156 $TEMPLATE $OPTIONS --description "AS156" \
    --net0,name=eth0,bridge=vmbr0,hwaddr=00:00:00:00:00:07,ip=dhcp,ip6=auto \
    --net1,name=eth1,bridge=pubnet,ip=dhcp,ip6=auto \
    --net2,name=sciB,bridge=scionB,ip6=fd00:fade:b::156/64 \
    --net3,name=sciD,bridge=scionD,ip6=fd00:fade:d::156/64 \
    --net4,name=sci12,bridge=scion12,ip6=fd00:fade:12::156/64 \
    --net5,name=sci17,bridge=scion17,ip6=fd00:fade:17::156/64

pct create 157 $TEMPLATE $OPTIONS --description "AS157" \
    --net0,name=eth0,bridge=vmbr0,hwaddr=00:00:00:00:00:08,ip=dhcp,ip6=auto \
    --net1,name=eth1,bridge=pubnet,ip=dhcp,ip6=auto \
    --net2,name=sciC,bridge=scionC,ip6=fd00:fade:c::157/64 \
    --net3,name=sciE,bridge=scionE,ip6=fd00:fade:e::157/64 \
    --net4,name=sci18,bridge=scion18,ip6=fd00:fade:18::157/64

pct create 158 $TEMPLATE $OPTIONS --description "AS158" \
    --net0,name=eth0,bridge=vmbr0,hwaddr=00:00:00:00:00:09,ip=dhcp,ip6=auto \
    --net1,name=eth1,bridge=pubnet,ip=dhcp,ip6=auto \
    --net2,name=sciF,bridge=scionF,ip6=fd00:fade:f::158/64 \
    --net3,name=sci11,bridge=scion11,ip6=fd00:fade:11::158/64 \
    --net4,name=sci13,bridge=scion13,ip6=fd00:fade:13::158/64

pct create 159 $TEMPLATE $OPTIONS --description "AS159" \
    --net0,name=eth0,bridge=vmbr0,hwaddr=00:00:00:00:00:0A,ip=dhcp,ip6=auto \
    --net1,name=eth1,bridge=pubnet,ip=dhcp,ip6=auto \
    --net2,name=sci13,bridge=scion13,ip6=fd00:fade:13::159/64 \
    --net3,name=sci14,bridge=scion14,ip6=fd00:fade:14::159/64

pct create 160 $TEMPLATE $OPTIONS --description "AS160" \
    --net0,name=eth0,bridge=vmbr0,hwaddr=00:00:00:00:00:0B,ip=dhcp,ip6=auto \
    --net1,name=eth1,bridge=pubnet,ip=dhcp,ip6=auto \
    --net2,name=sci15,bridge=scion15,ip6=fd00:fade:15::160/64

pct create 161 $TEMPLATE $OPTIONS --description "AS161" \
    --net0,name=eth0,bridge=vmbr0,hwaddr=00:00:00:00:00:0C,ip=dhcp,ip6=auto \
    --net1,name=eth1,bridge=pubnet,ip=dhcp,ip6=auto \
    --net2,name=sci16,bridge=scion16,ip6=fd00:fade:16::161/64 \
    --net3,name=sci17,bridge=scion17,ip6=fd00:fade:17::161/64 \
    --net4,name=sci18,bridge=scion18,ip6=fd00:fade:18::161/64

pct create 200 $TEMPLATE $OPTIONS --description "Dashboard" \
    --net0,name=eth0,bridge=vmbr0,hwaddr=00:00:00:00:00:C8,ip=10.20.3.200/24,ip6=auto \
    --net1,name=eth1,bridge=pubnet,ip=dhcp,ip6=auto

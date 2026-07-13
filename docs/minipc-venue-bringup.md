# Minipc venue-host bring-up: control node + WG endpoint sync

The full testbed is already deployed on `ietf-minipc-rack` (the venue-facing
production host) with its fixed internal `10.20.3.0/24` plane. Two things were
deliberately left for the day the minipc is plugged into the IETF network,
because they only matter once the WireGuard hub has a **real venue address**:

1. **Make the minipc host itself an ansible control node** so it can redeploy
   the dashboard on its own (the WG endpoint-sync mechanism runs `ansible` *on
   the host*, not from the dev box).
2. **Refresh the WG join endpoint** from the hub's current venue address so the
   confs handed to attendees point at the right place.

Facts this procedure relies on (verified 2026-07-13 on the host):

- Repo present at `/root/ietf-scion-testbed` (rsynced from the dev box, minus
  `.git`/`.build`). It has everything `deploy_dashboard.yaml` needs:
  `ansible/inventory.yaml`, `ansible/group_vars/playground.yml`, `config/`,
  `dashboard/backend/dist/fabricd_0.1.0_amd64.deb`, and the ansible files.
  `.build/` is **not** needed by `deploy_dashboard`.
- The host reaches the containers **directly** at `10.20.3.x` (it holds the
  `mgmt` gateway `10.20.3.1`), so on the host you use the plain
  `ansible/inventory.yaml` — **not** `inventory-minipc.yaml` (that one is the
  dev box's ProxyJump inventory).
- The containers' `ietf` user authorizes exactly the keys in
  `proxmox/public_keys` (installed by `bootstrap_ietf_user.yaml`).
- `ansible` (Debian 13 candidate `12.0.0`) is installable from apt.

Run everything below **on the minipc host** unless a step says "from the dev box".

---

## Item 1 — make the minipc host an ansible control node

### 1a. Install ansible on the host

```sh
sudo apt update && sudo apt install -y ansible python3-yaml
ansible --version | head -1
```

### 1b. Give host `root` an SSH key that the `ietf` user accepts

The endpoint-sync service runs as **root** on the host and connects to the
containers as `ietf`, so root needs a key whose public half is authorized for
`ietf`. Generate one and make `proxmox/public_keys` the source of truth (so a
container rebuild keeps it):

```sh
# on the minipc host
sudo ssh-keygen -t ed25519 -N '' -C minipc-host-ansible -f /root/.ssh/id_ed25519
sudo cat /root/.ssh/id_ed25519.pub
```

Then, **from the dev box**, append that exact public-key line to
`proxmox/public_keys`, sync it to the host, and re-authorize the running fleet.
Re-running the bootstrap play is the simplest builtin-only way — it rewrites
each container's `/home/ietf/.ssh/authorized_keys` to match `public_keys`:

```sh
# from the dev box, in the repo root
$EDITOR proxmox/public_keys        # add the minipc-host-ansible pubkey line
rsync -a --rsync-path="sudo rsync" --exclude '.git' --exclude '.build' \
      ./ ietf-minipc-rack:/root/ietf-scion-testbed/
ansible-playbook -i ansible/inventory-minipc.yaml -e ansible_user=root \
      ansible/playbooks/bootstrap_ietf_user.yaml     # idempotent; re-writes authorized_keys
```

Verify from the host that root can reach a container as `ietf`:

```sh
sudo ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new ietf@10.20.3.200 hostname
```

### 1c. Turn off strict host-key checking for root's ansible runs

There is no `ansible.cfg` in the repo, so ansible defaults to
`host_key_checking = True` and a non-interactive run would fail on the
containers' (unknown, rebuild-changeable) host keys. Set a host-global config
that survives repo re-syncs (`rsync --delete` would wipe a repo-local
`ansible.cfg`) and container rebuilds:

```sh
sudo mkdir -p /etc/ansible
printf '[defaults]\nhost_key_checking = False\n' | sudo tee /etc/ansible/ansible.cfg
```

### 1d. Smoke-test the control node (before the venue, against rack DHCP)

```sh
sudo FORCE=1 bash /root/ietf-scion-testbed/tools/update-wg-endpoint.sh
```

Expected on a **rack** connection: it reads CT201 `eth1`, and
- if the rack hands out a global IPv6 (SLAAC): it writes `wg_endpoint_v4/v6`
  into the host's `group_vars/playground.yml` and reruns `deploy_dashboard.yaml`
  (`failed=0`);
- if the rack is IPv4-only: it prints `hub eth1 has no global IPv6 yet` and
  exits (a manual run treats that as an error; the timer's `AUTO=1` soft-skips
  it). That is expected off-venue — the venue net is v6-mostly, so this
  succeeds once plugged in.

This step proves 1a–1c are correct even before the venue.

### 1e. Install and enable the endpoint-sync timer

```sh
sudo cp /root/ietf-scion-testbed/proxmox/wg-endpoint-sync.service /etc/systemd/system/
sudo cp /root/ietf-scion-testbed/proxmox/wg-endpoint-sync.timer   /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now wg-endpoint-sync.timer
systemctl status wg-endpoint-sync.timer --no-pager | head -4   # expect: active (waiting)
```

The service fires ~2 min after boot then every 5 min. Each run is a cheap no-op
unless the hub's venue address changed (the script is idempotent), and it
soft-skips (`AUTO=1`) while the hub has no venue address — so it will not flap a
failed unit before the venue plug-in.

---

## Item 2 — refresh the WG join endpoint after plugging into the venue

Once the minipc is on the IETF network and CT201 (`wg-hub`) has a venue `eth1`
lease (IPv4 + a global-unicast IPv6 via SLAAC):

- **Hands-free:** the timer (1e) runs `update-wg-endpoint.sh` within 5 minutes,
  detects the changed endpoint, and redeploys the dashboard so
  `/api/join/meta` advertises the venue endpoint.
- **Immediately / by hand:**

  ```sh
  sudo bash /root/ietf-scion-testbed/tools/update-wg-endpoint.sh      # or FORCE=1 to redeploy even if unchanged
  ```

  It prefers a real global-unicast v6 (`2000::/3`) over a ULA, never a
  temporary/privacy address, and keeps v4 as a fallback.

### Verify

```sh
# the derived addresses the script picked
sudo pct exec 201 -- ip -4 -o addr show eth1 | awk '{print $4}'
sudo pct exec 201 -- ip -6 -o addr show eth1 scope global | grep -v temporary

# the join meta now advertises them (booth_code is in group_vars/playground.yml)
curl -su scion:<booth_code> http://<dashboard-venue-addr>:8080/api/join/meta | grep -o '"endpoint_v6":"[^"]*"'
```

`endpoint_v6` in the meta should match CT201's global v6. The endpoints are
injected into each conf **at claim time**, so only `deploy_dashboard.yaml` needs
rerunning (the script does it) — the hub's own `wg0.conf` is untouched.

### Sync-direction caution

`update-wg-endpoint.sh` edits the **host** copy of `group_vars/playground.yml`.
If you later re-rsync the repo from the dev box with `--delete`, you will
overwrite the host's venue endpoints with the dev box's stale values — a silent
rollback that breaks attendees' `wg-quick up`. After venue day, either do not
rsync `group_vars` over the host, or copy the updated `wg_endpoint_v4/v6` back
into the dev box's `ansible/group_vars/playground.yml` so both agree.

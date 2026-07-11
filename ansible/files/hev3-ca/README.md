# hev3 testbed CA (committed key material — testbed only)

This directory holds a throwaway certificate authority and the per-name server
certificates the `hev3-server` demo target presents. **These private keys are
deliberately committed to the repo.** They exist only so the SCION-in-a-Box
testbed can stand up end-to-end TLS without a secret-management dance. They
protect nothing real — never reuse them, never trust this CA outside the
testbed.

## Layout

- `ca.pem` / `ca.key` — the CA cert (10y, CN "SCION-in-a-Box hev3 CA") and key.
  `ca.pem` ships in the `scion-hev3` deb at `/etc/hev3/ca.pem` and is the root
  the `hev3` CLI trusts by default.
- `web.scion/`, `web2.scion/` — `{cert.pem,key.pem}` per demo host, each a 2y
  leaf with `subjectAltName = DNS:<name>`, signed by the CA. Ansible deploys the
  matching pair to each `hev3-server` host as `/etc/hev3/cert.pem` and
  `/etc/hev3/key.pem`.

## Regenerating

```
tools/gen-hev3-ca.sh
```

Idempotent: an existing `ca.pem` is reused and existing per-name certs are left
alone. To rotate, delete the file(s) you want rebuilt and re-run. Rotating the
CA means re-signing every leaf (delete the `<name>/` dirs too) and rebuilding /
redeploying the deb so hosts pick up the new `ca.pem`.

# Tsnet / Tailscale

Both `cairo serve` and `cairo-registry` can bind via Tailscale's embedded networking library (tsnet) instead of opening a plain TCP port. This gives each process a stable Tailscale hostname with TLS, reachable anywhere in the tailnet, without exposing a public port.

---

## Why tsnet

- No firewall rules needed — the process registers a node in your tailnet and accepts connections over the Tailscale overlay network.
- TLS is provided by Tailscale (Let's Encrypt via Tailscale's DNS), not by the operator.
- Operator identity is available on a per-request basis via `WhoIs()` — cairo-registry uses this to scope agent ownership to the Tailscale identity of the connecting node.
- The node appears in `tailscale status` alongside your other machines.

---

## `cairo serve --tsnet`

Starts the cairo HTTP server on `:443` within the tailnet.

```sh
cairo serve --tsnet
```

The tsnet hostname is derived from the system hostname with the prefix `cairo-`:

```
cairo-myhost.your-tailnet.ts.net
```

Non-alphanumeric characters in the hostname are replaced with `-`; the result is lowercased and truncated to 63 characters.

State is stored in `~/.cairo/tsnet/`. This directory is created on first run with mode `0700`.

### First run: authorization

On first run, cairo prints a Tailscale LoginURL to stdout:

```
  authorize this node: https://login.tailscale.com/a/...
```

Visit the URL in a browser while signed in to your Tailscale account to authorize the node. Once authorized, cairo continues startup and prints:

```
cairo server listening via tailnet
  url:   https://cairo-myhost.your-tailnet.ts.net
  token: (none — open)
```

Subsequent restarts skip authorization — the authorized state persists in `~/.cairo/tsnet/`.

### With auth

```sh
cairo serve --tsnet --auth
```

```
cairo server listening via tailnet
  url:   https://cairo-myhost.your-tailnet.ts.net
  token: 3f8a1c2d9e4b7f60
```

Bearer tokens work identically in tsnet mode. See [authentication.md](authentication.md).

### With registry

`--tsnet` and `--register` are independent flags. An agent can serve over tsnet and register with a tsnet-based registry:

```sh
cairo serve --tsnet --register https://cairo-registry.your-tailnet.ts.net
```

---

## `cairo-registry` (tsnet mode)

cairo-registry uses tsnet by default. The tsnet node is named `cairo-registry`:

```
cairo-registry.your-tailnet.ts.net
```

State is stored in `{state-dir}/tsnet/` (default `~/.cairo-registry/tsnet/`).

```sh
cairo-registry -state-dir /var/lib/cairo-registry
```

On first run, same authorization flow — a LoginURL is printed to stdout.

The admin listener (`127.0.0.1:8081`) is always plain TCP, regardless of tsnet mode. tsnet affects only the public listener.

To disable tsnet (local dev / CI):

```sh
cairo-registry -no-tsnet -addr :8080
```

---

## Verifying node status

After startup, verify the node is visible in the tailnet:

```sh
tailscale status
```

The cairo node should appear with its hostname. To verify it is reachable:

```sh
curl https://cairo-myhost.your-tailnet.ts.net/healthz
# → {"status":"ok"}
```

---

## Auth key (pre-authorization)

There is no `CAIRO_TSNET_AUTHKEY` environment variable in the current implementation. Authorization is interactive on first run: cairo prints a LoginURL, the operator visits it, and the node state is persisted.

For headless / unattended first runs (CI, provisioning scripts), use the [Tailscale API](https://tailscale.com/kb/1101/api/) to pre-create an auth key and set it in the tsnet `Server.AuthKey` field — this requires a code-level change or a future flag. File an issue if this is blocking your deployment.

---

## Limitations

- `--tsnet` and `--port` are mutually exclusive. In tsnet mode, the port flag is ignored — tsnet always binds TLS on `:443`.
- tsnet requires an active Tailscale tailnet. The process cannot start in tsnet mode if network authorization fails.
- tsnet node names must be unique within the tailnet. If you run multiple cairo instances, their hostnames will differ (each machine gets `cairo-{hostname}`), but cairo-registry always uses the fixed name `cairo-registry` — run at most one per tailnet.
- Tailscale ACLs apply. If your tailnet ACL policy does not permit the connecting device to reach the cairo node on `:443`, connections will fail at the Tailscale layer.

---

## Revoking a tsnet node

If a machine is decommissioned, remove its tsnet node from the tailnet admin console to prevent stale entries. The tsnet state directory (`~/.cairo/tsnet/` or `{state-dir}/tsnet/`) can be deleted to force re-authorization on the next start.

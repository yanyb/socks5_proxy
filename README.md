# my_socks5_proxy

SOCKS5 proxy on the cloud, **egress through a phone (or any device)** over **TLS + yamux**. The SOCKS5 protocol is handled by [things-go/go-socks5](https://github.com/things-go/go-socks5); the phone link uses [hashicorp/yamux](https://github.com/hashicorp/yamux).

## Layout

- `server/` — listens for SOCKS5 users and for device connections (TLS, then yamux). Subpackages: `server/hub`, `server/config`, `server/configs`.
- `client/` — runs on the device: registers, heartbeats, dials targets when the server opens streams. Subpackages: `client/core`, `client/config`, `client/configs`.
- `mobile/` — gomobile bind target (Android AAR) that reuses `client/core`.
- `common/` — shared code used by multiple services: `common/logger` (logrus + lumberjack), `common/protocol` (NDJSON envelope, `aescbc`, `heartbeat` event schema).
- `admin/` — admin/control plane (IP whitelist, auth, quotas — WIP).
- `third_party/` — local forks of `go-socks5` and `yamux` referenced by `go.mod` via `replace`.
- `server/configs/server.example.yaml` — server-only settings; copy to `server/configs/server.yaml`.
- `client/configs/client.example.yaml` — device-only settings (YAML or JSON); copy to `client/configs/client.yaml` or use `.json` after API download.

Server and client configs are **separate files**. Device-side fields are tagged for **JSON** (`client/config.Client`) so you can:

- Save an API response to a file and run `go run ./client -config /path/to/device.json` (extension **`.json`** selects JSON parsing).
- Or call `config.ParseClientJSON(body)` in your own bootstrap code.

## Quick start (local dev)

1. Copy configs and create TLS material:

```bash
cd /path/to/my_socks5_proxy
cp server/configs/server.example.yaml server/configs/server.yaml
cp client/configs/client.example.yaml client/configs/client.yaml
openssl req -x509 -newkey rsa:2048 -keyout server/configs/server.key -out server/configs/server.crt -days 365 -nodes -subj "/CN=localhost"
```

2. Start the server (two listeners: SOCKS5 + device TLS):

```bash
go run ./server -config server/configs/server.yaml
```

3. Start the client (device):

```bash
go run ./client -config client/configs/client.yaml
```

4. Point a SOCKS5 client at `127.0.0.1:1080`. With no `socks_auth_password` and **only one** phone online, that phone is used; with multiple phones, set `socks_auth_password` and use SOCKS username = `device_id`.

## Multiple phones (device selection)

Online sessions are tracked in `hub.Registry` (`ListOnline()` for your future admin API or routing policy).

**How the user picks a phone for SOCKS5:**

1. Set `socks_auth_password` on the server to a shared secret.
2. Configure the user’s SOCKS client with **username = `device_id`** of the phone and **password = `socks_auth_password`**.
3. Selection logic is in `Registry.ResolveDeviceForDial`: explicit SOCKS username (`socks_auth_password` enabled), or if no-auth and **exactly one** device is online, that device is used. Multiple devices with no-auth returns an error until you enable user/pass (you can later plug in a custom strategy at this call site).

Without `socks_auth_password`, SOCKS5 has no username field — routing works only when a single phone is connected.

## Protocol (application framing)

On each yamux stream, the first lines are **newline-delimited JSON** (`protocol`):

- Control stream (opened first by the device): `register` → `register_ack`, then periodic `heartbeat` / `heartbeat_ack`.
- CONNECT streams (opened by the server): `connect` → `connect_result`, then raw TCP relay.

Yamux keepalive is enabled; optional `session_heartbeat_timeout` on the server drops the session if app-level heartbeats stop for too long.

The device client **reconnects automatically** after any failure or dropped session, using **exponential backoff with jitter** (`reconnect_initial_backoff`, `reconnect_max_backoff` in client config; backoff resets after a successful registration).

## Build

```bash
go build -o bin/server ./server
go build -o bin/client ./client
```

## Notes

- Only **TCP CONNECT** egress is implemented; other SOCKS5 commands return errors from the custom dial path.
- Each phone uses its own `device_id` in `client` config; the SOCKS user typically enters that same id as username when `socks_auth_password` is set.
- The device client uses TLS to the server but **does not verify the server certificate** (trust server); traffic is still encrypted on the wire.

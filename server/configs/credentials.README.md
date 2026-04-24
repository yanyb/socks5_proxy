# SOCKS5 credentials format

The credentials file is a flat JSON map of `key -> password`. The key shape
depends on `socks_username_scheme` in `server.yaml`:

## `socks_username_scheme: device_id` (default, legacy)

The SOCKS5 username **is** the device_id. One entry per device:

```json
{
  "phone-1": "s3cret-for-phone-1",
  "phone-2": "s3cret-for-phone-2"
}
```

## `socks_username_scheme: scheduler`

The SOCKS5 username is `B_<user_id>_<country>_<duration>_<session>`, e.g.
`B_38313_US_5_Ab000001`. The credentials key is the auth key
`<user_id>:<session>` (country/duration are scheduling parameters and never
appear in the key). One entry per "代理账号":

```json
{
  "B_38313:Ab000001": "passwd-for-account-1",
  "B_38313:Ab000002": "passwd-for-account-2"
}
```

Rotating country or duration in the SOCKS5 username does **not** require
adding a new credential entry — the same account works across all (country,
duration) combinations.

## Operational notes

- File is re-read every `socks_credentials_refresh` (default `1m`); edit on
  disk and the next refresh picks it up.
- A failed refresh keeps the previous snapshot — admin outage must not lock
  users out.
- Both schemes can coexist in a single file (separate keys). Useful during
  migration.

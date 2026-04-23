# server/data

Drop runtime data files here. The contents are NOT committed.

## GeoLite2-City.mmdb

The server reads this file to enrich heartbeat events with country/region
before publishing to NSQ.

- Source: https://github.com/wp-statistics/GeoLite2-City (auto-updated; CC BY-SA 4.0)
- CDN URL: `https://cdn.jsdelivr.net/npm/geolite2-city/GeoLite2-City.mmdb.gz`
- Update cadence: maintainers refresh every Tue/Fri.

### Refresh

```sh
# from repo root
./scripts/update-geoip.sh
kill -HUP $(pgrep -f xsocks5-server)   # hot-reload without restart
```

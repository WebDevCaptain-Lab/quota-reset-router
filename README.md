# quota-reset-router

A [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) scheduler plugin for Claude and Codex OAuth accounts. It routes each request to the eligible account whose **weekly quota resets soonest**, so quota that would otherwise expire unused is consumed first.

- **Author:** Shreyash ([webdevcaptain](https://github.com/webdevcaptain))
- **License:** [MIT](LICENSE)

> Not affiliated with or endorsed by Anthropic, OpenAI, or CLIProxyAPI. Check each provider's terms before routing subscription credentials through a proxy.

## Selection rules

- Claude and Codex pools are ranked independently. Requests spanning multiple providers are left to CLIProxyAPI.
- **Rank:** earliest weekly reset first. Ties are ordered by credential ID.
- **Skip:** accounts whose five-hour, weekly, or model-specific (Sonnet/Opus) quota is exhausted.
- Existing credential `priority` tiers still take precedence.
- Only candidates offered by CLIProxyAPI are considered, so its model eligibility, cooldowns, and retries still apply.
- Codex: the earliest reset among its weekly or monthly windows is used for ranking.

Example: account A (weekly reset in 20 hours) is chosen over account B (weekly reset in 4 days), even if B's five-hour window resets in 2 hours. If A is exhausted, B is chosen.

## Quota polling

- One background worker. Request routing uses cached data and makes no network calls.
- Refreshes every `poll_interval` (default 5 minutes) and shortly after any reported reset. Checks for credential changes every 30 seconds.
- Read-only `GET` requests to:
  - `https://api.anthropic.com/api/oauth/usage`
  - `https://chatgpt.com/backend-api/wham/usage`
- No model requests, token refreshes, or credential writes.

## Fallback

The plugin hands the request back to CLIProxyAPI's configured routing strategy when it has no trustworthy data:

- before the first successful quota refresh
- after a 401 or 403 from a quota endpoint
- when cached quota is older than `max_age` (other refresh errors keep the last good data until then)
- when no eligible account has known quota

Ordering is not guaranteed in these cases. Quota can also change between refreshes.

## Requirements

- CLIProxyAPI v7.3.15 (plugin ABI 1, schema 6). Other versions are untested.
- Linux amd64 with glibc 2.36 or newer.
- Direct network access to both quota endpoints. Credentials with `proxy_url` or `base_url` are skipped. Quota polling ignores CLIProxyAPI's `proxy-url` and proxy environment variables.

## Build

Requires Docker.

```sh
make linux-build   # writes dist/quota-reset-router.so
```

## Install

1. Copy `dist/quota-reset-router.so` into CLIProxyAPI's plugin directory (`plugins.dir`) as a regular file. Symlinks are not loaded.
2. Merge into `config.yaml`:

   ```yaml
   plugins:
     enabled: true
     dir: plugins
     configs:
       quota-reset-router:
         enabled: true
         priority: 10
         mode: shadow
   ```

3. Restart CLIProxyAPI.
4. Review the status endpoint. When the proposed choices are correct, switch `mode` to `active`.

`priority` orders plugins, not accounts. CLIProxyAPI consults only the highest-priority scheduler plugin.

## Configuration

| Key | Default | Allowed | Purpose |
|---|---|---|---|
| `mode` | `shadow` | `shadow`, `active` | `shadow` records proposed choices only. `active` routes requests. |
| `poll_interval` | `5m` | `1m` to `1h` | Quota refresh interval. |
| `max_age` | `10m` | `poll_interval` to `1h` | Maximum age of cached quota. |
| `request_timeout` | `10s` | `1s` to `30s` | Timeout per quota request. |

Change settings without a restart:

```text
PATCH /v0/management/plugins/quota-reset-router/config
{"mode": "active"}
```

## Status

```text
GET /v0/management/plugins/quota-reset-router/status
```

Requires Management API authentication. Returns the version, mode, selection policy, per-account quota snapshots, refresh errors, the last decision, and routing counters. Credential IDs are included (often file names containing email addresses). OAuth tokens are not.

## Disable or upgrade

- **Disable** without a restart. CLIProxyAPI's configured routing resumes after it reloads the configuration:

  ```text
  PATCH /v0/management/plugins/quota-reset-router/enabled
  {"enabled": false}
  ```

- **Upgrade:** stop CLIProxyAPI, replace the `.so`, start CLIProxyAPI.

## Limitations

- Native plugins run inside the CLIProxyAPI process with access to its credentials and traffic. Expected errors fall back to CLIProxyAPI routing; a native crash can still affect the proxy.
- When the plugin selects an account, CLIProxyAPI's built-in strategy, including session affinity, is not used for that request.
- CLIProxyAPIHome dispatch does not consult plugin schedulers.
- The quota endpoints are not stable public APIs. Unrecognized responses trigger fallback.

## Development

Local tests require Go 1.26+ and a C toolchain. Container targets require Docker.

```sh
make test          # unit tests with the race detector
make linux-test    # same, in the pinned Linux container
make linux-build
make native-test   # loads the built .so through the plugin C ABI
```

Integration test against the official CLIProxyAPI release:

```sh
gh release download v7.3.15 --repo router-for-me/CLIProxyAPI \
  --pattern CLIProxyAPI_7.3.15_linux_amd64.tar.gz \
  --pattern checksums.txt --dir dist
make host-test
```

Native and integration tests run with networking disabled, local TLS fixtures, and synthetic credentials. `make clean` removes build output.

## Release

The CLIProxyAPI plugin store installs from this repository's latest GitHub release.

1. Set `pluginVersion` in `config.go`, for example `0.2.0`.
2. Run `make package`. It writes `dist/release/quota-reset-router_<version>_linux_amd64.zip` (plugin, `LICENSE`, `THIRD_PARTY_NOTICES.md`) and `dist/release/checksums.txt`.
3. Publish a GitHub release tagged `v<version>` with both files attached.

## Third-party notices

Binary releases link the CLIProxyAPI plugin SDK (MIT), `gopkg.in/yaml.v3` (MIT and Apache-2.0), and the Go standard library (BSD-3-Clause). See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

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
- Linux amd64 or arm64 (glibc 2.34 or newer), macOS amd64 or arm64, or Windows amd64.
- Direct network access to both quota endpoints. Credentials with `proxy_url` or `base_url` are skipped. Quota polling ignores CLIProxyAPI's `proxy-url` and proxy environment variables.

## Install

1. Download `quota-reset-router_<version>_<os>_<arch>.zip` for your platform from the [latest release](https://github.com/WebDevCaptain/quota-reset-router/releases/latest) and check it against `checksums.txt`. Optionally verify its build provenance: `gh attestation verify <zip> --repo WebDevCaptain/quota-reset-router`.
2. Extract `quota-reset-router.so` (Linux), `quota-reset-router.dylib` (macOS), or `quota-reset-router.dll` (Windows) into CLIProxyAPI's plugin directory (`plugins.dir`) as a regular file. Symlinks are not loaded.
3. Merge into `config.yaml`:

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

4. Restart CLIProxyAPI.
5. Review the status endpoint. When the proposed choices are correct, switch `mode` to `active`.

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

- **Upgrade:** stop CLIProxyAPI, replace the plugin file, start CLIProxyAPI.

## Limitations

- Native plugins run inside the CLIProxyAPI process with access to its credentials and traffic. Expected errors fall back to CLIProxyAPI routing; a native crash can still affect the proxy.
- When the plugin selects an account, CLIProxyAPI's built-in strategy, including session affinity, is not used for that request.
- CLIProxyAPIHome dispatch does not consult plugin schedulers.
- The quota endpoints are not stable public APIs. Unrecognized responses trigger fallback.

## Development

Build and test targets take `TARGET`: `linux_amd64` (default), `linux_arm64`, `darwin_amd64`, `darwin_arm64`, or `windows_amd64`.

- Linux and Windows builds, `linux-test`, `native-test`, and `host-test` run in a pinned Docker image.
- macOS builds need a macOS host with Go 1.21+ and the Xcode Command Line Tools. Go 1.26.8 is downloaded automatically.
- `make test` needs Go 1.26+ and a C toolchain.

```sh
make test         # gofmt, go vet, and unit tests with the race detector
make linux-test   # same, in the pinned Linux container
make build        # writes dist/<target>/quota-reset-router.<so|dylib|dll>
make native-test  # Linux only: loads the library through the plugin C ABI
make host-test    # Linux only: runs the official CLIProxyAPI release with the library
make zip          # writes dist/release/quota-reset-router_<version>_<target>.zip
make load-test    # loads the zip into the official CLIProxyAPI; TARGET must match this machine
```

- `host-test` and `load-test` download the official CLIProxyAPI v7.3.15 release and verify its checksum.
- `native-test` and `host-test` run with networking disabled, local TLS fixtures, and synthetic credentials.
- `load-test` starts CLIProxyAPI without credentials, so the plugin makes no network requests.
- CI builds all five targets and loads each zip into the official CLIProxyAPI on its own platform.
- `make clean` removes build output.

## Release

The CLIProxyAPI plugin store installs from this repository's latest published GitHub release. Each release contains `checksums.txt` and one `quota-reset-router_<version>_<os>_<arch>.zip` per target, holding the plugin, `LICENSE`, and `THIRD_PARTY_NOTICES.md`.

1. Set `pluginVersion` in `config.go`, for example `0.2.0`, and merge to `main`.
2. Push the tag `v<version>`. CI builds and tests every target, attests build provenance, and creates a draft release with the assets.
3. Review the draft and publish it.

`make release-assets` builds the same set locally on a macOS host with Docker.

## Third-party notices

Binary releases link the CLIProxyAPI plugin SDK (MIT), `gopkg.in/yaml.v3` (MIT and Apache-2.0), and the Go standard library (BSD-3-Clause). Windows builds also statically link parts of the MinGW-w64 runtime. See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

#!/usr/bin/env bash
# Probe: start the official CLIProxyAPI macOS build with one plugin at a time and report whether it survives.
set -u
V=7.3.15
case "$(uname -m)" in
  x86_64) ARCH=amd64 CPA_ARCH=amd64 ;;
  arm64) ARCH=arm64 CPA_ARCH=aarch64 ;;
esac
WORK=$(mktemp -d)

summary() {
  printf '%s\n' "$1"
  if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then printf '%s\n' "$1" >> "$GITHUB_STEP_SUMMARY"; fi
}

curl -fsSL -o "$WORK/cpa.tgz" "https://github.com/router-for-me/CLIProxyAPI/releases/download/v$V/CLIProxyAPI_${V}_darwin_${CPA_ARCH}.tar.gz"
curl -fsSL -o "$WORK/checksums.txt" "https://github.com/router-for-me/CLIProxyAPI/releases/download/v$V/checksums.txt"
(cd "$WORK" && grep " CLIProxyAPI_${V}_darwin_${CPA_ARCH}.tar.gz\$" checksums.txt | sed "s/ CLIProxyAPI_.*/ cpa.tgz/" | shasum -a 256 -c -) || exit 1
tar -xzf "$WORK/cpa.tgz" -C "$WORK" cli-proxy-api

CGO_ENABLED=1 go build -trimpath -buildvcs=false -buildmode=c-shared -o "$WORK/quota-reset-router-v0.2.0.dylib" .
git clone -q --depth 1 --branch "v$V" https://github.com/router-for-me/CLIProxyAPI "$WORK/cpa-src"
(cd "$WORK/cpa-src/examples/plugin/scheduler/go" && CGO_ENABLED=1 go build -buildmode=c-shared -o "$WORK/scheduler.dylib" .)
cmake -S "$WORK/cpa-src/examples/plugin/management-api/c" -B "$WORK/build-c" -DCMAKE_LIBRARY_OUTPUT_DIRECTORY="$WORK/c-out" >/dev/null
cmake --build "$WORK/build-c" >/dev/null
cp "$WORK/c-out/management-api-c.dylib" "$WORK/example-management-api-c.dylib"

summary "### CLIProxyAPI v$V darwin_$CPA_ARCH on macOS $(sw_vers -productVersion) ($(uname -m))"
summary ""
summary "| Plugin | Language | Result |"
summary "|---|---|---|"

run_case() {
  local label=$1 lang=$2 lib=$3 id=$4 d pid code result fatal
  d=$(mktemp -d)
  mkdir -p "$d/plugins/darwin/$ARCH" "$d/auths"
  cp "$lib" "$d/plugins/darwin/$ARCH/"
  cat > "$d/config.yaml" <<EOF
host: 127.0.0.1
port: 18400
auth-dir: "$d/auths"
api-keys: [probe-client-key]
remote-management:
  allow-remote: false
  secret-key: probe-management-key
  disable-control-panel: true
plugins:
  enabled: true
  dir: "$d/plugins"
  configs:
    $id:
      enabled: true
EOF
  (cd "$d" && exec "$WORK/cli-proxy-api" --config "$d/config.yaml" --local-model > "$d/cpa.log" 2>&1) &
  pid=$!
  sleep 20
  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid"; wait "$pid"
    if grep -q "plugin registered plugin_id=$id " "$d/cpa.log"; then
      result="Registered; CLIProxyAPI still running after 20 s"
    else
      result="Not registered; CLIProxyAPI still running after 20 s"
    fi
  else
    wait "$pid"; code=$?
    fatal=$(grep -m1 '^fatal error:' "$d/cpa.log" || true)
    result="CLIProxyAPI exited with code $code${fatal:+: \`$fatal\`}"
  fi
  echo "::group::$label log"
  cat "$d/cpa.log"
  echo "::endgroup::"
  summary "| $label | $lang | $result |"
}

run_case "quota-reset-router v0.2.0" Go "$WORK/quota-reset-router-v0.2.0.dylib" quota-reset-router
run_case "CLIProxyAPI v$V examples/plugin/scheduler" Go "$WORK/scheduler.dylib" scheduler
run_case "CLIProxyAPI v$V examples/plugin/management-api" C "$WORK/example-management-api-c.dylib" example-management-api-c

#!/usr/bin/env bash
# Probe: load Go plugins built with stock Go 1.26.8 and with the TLS-slot patch into the official CLIProxyAPI macOS build.
set -u
V=7.3.15
GO_V=1.26.8
GO_SRC_SHA256=4e39b98e42f946fa05ac8bc5b71877df97dbdb7cbb1a777b541667ad7117fd2e
case "$(uname -m)" in
  x86_64) ARCH=amd64 CPA_ARCH=amd64 ;;
  arm64) ARCH=arm64 CPA_ARCH=aarch64 ;;
esac
WORK=${WORK:-$(mktemp -d)}
REPO=$(pwd)
export WORK ARCH CPA_ARCH

summary() {
  printf '%s\n' "$1"
  if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then printf '%s\n' "$1" >> "$GITHUB_STEP_SUMMARY"; fi
}

CPA_TGZ="$WORK/CLIProxyAPI_${V}_darwin_${CPA_ARCH}.tar.gz"
curl -fsSL -o "$CPA_TGZ" "https://github.com/router-for-me/CLIProxyAPI/releases/download/v$V/CLIProxyAPI_${V}_darwin_${CPA_ARCH}.tar.gz"
curl -fsSL -o "$WORK/checksums.txt" "https://github.com/router-for-me/CLIProxyAPI/releases/download/v$V/checksums.txt"
(cd "$WORK" && grep " CLIProxyAPI_${V}_darwin_${CPA_ARCH}.tar.gz\$" checksums.txt | shasum -a 256 -c -) || exit 1
tar -xzf "$CPA_TGZ" -C "$WORK" cli-proxy-api

# Patched toolchain: upstream source, checksum-pinned, plus debug/go-tls-slot.patch.
curl -fsSL -o "$WORK/go-src.tgz" "https://go.dev/dl/go${GO_V}.src.tar.gz"
echo "$GO_SRC_SHA256  $WORK/go-src.tgz" | shasum -a 256 -c - || exit 1
mkdir -p "$WORK/gotls" && tar -xzf "$WORK/go-src.tgz" -C "$WORK/gotls"
patch -d "$WORK/gotls/go" -p1 < "$REPO/debug/go-tls-slot.patch" || exit 1
(cd "$WORK/gotls/go/src" && GOROOT_BOOTSTRAP=$(go env GOROOT) ./make.bash) || exit 1
PATCHED_GO="$WORK/gotls/go/bin/go"
"$PATCHED_GO" version

git clone -q --depth 1 --branch "v$V" https://github.com/router-for-me/CLIProxyAPI "$WORK/cpa-src"

build() { # <go> <srcdir> <out>
  (cd "$2" && GOTOOLCHAIN=local CGO_ENABLED=1 "$1" build -trimpath -buildvcs=false -buildmode=c-shared -o "$3" .) || exit 1
  rm -f "${3%.dylib}.h"
}
build go "$REPO" "$WORK/stock/quota-reset-router.dylib"
build "$PATCHED_GO" "$REPO" "$WORK/patched/quota-reset-router.dylib"
build go "$WORK/cpa-src/examples/plugin/scheduler/go" "$WORK/stock/scheduler.dylib"
build "$PATCHED_GO" "$WORK/cpa-src/examples/plugin/scheduler/go" "$WORK/patched/scheduler.dylib"

slots() { otool -tvV "$1" | grep -o '%gs:0x[0-9a-f]*' | sort | uniq -c | awk '{printf "%s×%s ", $2, $1}'; }

summary "### CLIProxyAPI v$V darwin_$CPA_ARCH on macOS $(sw_vers -productVersion) ($(uname -m))"
summary ""
summary "| Plugin | Toolchain | GS accesses | Result |"
summary "|---|---|---|---|"

run_case() {
  local label=$1 tc=$2 lib=$3 id=$4 d pid code result fatal
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
  echo "::group::$label ($tc) log"
  cat "$d/cpa.log"
  echo "::endgroup::"
  summary "| $label | $tc | $(slots "$lib")| $result |"
}

for tc in stock patched; do
  run_case "quota-reset-router" "$tc" "$WORK/$tc/quota-reset-router.dylib" quota-reset-router
  run_case "CPA examples/plugin/scheduler (Go)" "$tc" "$WORK/$tc/scheduler.dylib" scheduler
done

summary ""
summary "#### Full host smoke (patched toolchain)"
if python3 tests/host_smoke.py "$CPA_TGZ" "$WORK/checksums.txt" "$WORK/patched/quota-reset-router.dylib" > "$WORK/smoke.out" 2>&1; then
  summary "$(grep '^PASS' "$WORK/smoke.out")"
else
  summary "FAIL: $(tail -1 "$WORK/smoke.out")"
  echo "::group::host smoke output"; cat "$WORK/smoke.out"; echo "::endgroup::"
  exit 1
fi

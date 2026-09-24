IMAGE := golang:1.26-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d
GO_VERSION := 1.26.8
CPA_VERSION := 7.3.15
PLUGIN := quota-reset-router
VERSION := $(shell sed -n 's/^const pluginVersion = "\(.*\)"/\1/p' config.go)
TARGETS := linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64

TARGET ?= linux_amd64
TARGET_OS := $(word 1,$(subst _, ,$(TARGET)))
TARGET_ARCH := $(word 2,$(subst _, ,$(TARGET)))
EXT := $(if $(filter windows,$(TARGET_OS)),dll,$(if $(filter darwin,$(TARGET_OS)),dylib,so))
LIB := dist/$(TARGET)/$(PLUGIN).$(EXT)
ZIP := dist/release/$(PLUGIN)_$(VERSION)_$(TARGET).zip
CPA_ARCH := $(if $(filter arm64,$(TARGET_ARCH)),aarch64,$(TARGET_ARCH))
CPA_ASSET := CLIProxyAPI_$(CPA_VERSION)_$(TARGET_OS)_$(CPA_ARCH).$(if $(filter windows,$(TARGET_OS)),zip,tar.gz)
CPA_URL := https://github.com/router-for-me/CLIProxyAPI/releases/download/v$(CPA_VERSION)
# -buildvcs=false: output depends only on the sources, and git in the build container rejects the mounted checkout's owner.
GO_BUILD := -trimpath -buildvcs=false -buildmode=c-shared -ldflags="-s -w"
CHECKS := test -z "$$(gofmt -l . | tee /dev/stderr)" && go vet ./... && go test -race -count=1 ./...
# THIRD_PARTY_NOTICES.md names this runtime version, so the Windows build fails if the image installs another.
MINGW_VERSION := 10.0.0-3
# macOS 12 is the oldest release Go 1.26 supports. Passed as CGO flags because the Go build cache ignores MACOSX_DEPLOYMENT_TARGET.
MACOS_MIN := -mmacosx-version-min=12.0

docker = docker run --rm --platform linux/$(1) -v "$(CURDIR):/src" -v $(PLUGIN)-go-mod:/go/pkg/mod -v $(PLUGIN)-go-build-$(1):/root/.cache/go-build -w /src
ISOLATED := --network none --add-host api.anthropic.com:127.0.0.1 --add-host chatgpt.com:127.0.0.1 -e PLUGIN_ISOLATED_TEST=1
LINUX_ONLY = $(if $(filter linux,$(TARGET_OS)),,$(error $@ loads the plugin in a Linux container; set TARGET to linux_amd64 or linux_arm64))

ifeq ($(filter $(TARGET),$(TARGETS)),)
$(error TARGET must be one of: $(TARGETS))
endif

.PHONY: test linux-test build zip checksums release-assets native-test host-test load-test cpa version clean

test:
	$(CHECKS)

linux-test:
	$(call docker,$(TARGET_ARCH)) $(IMAGE) sh -c '$(CHECKS)'

# Linux and Windows builds run in the pinned image; macOS builds need a macOS host.
build:
	mkdir -p dist/$(TARGET)
ifeq ($(TARGET_OS),linux)
	$(call docker,$(TARGET_ARCH)) $(IMAGE) sh -c 'CGO_ENABLED=1 go build $(GO_BUILD) -o $(LIB) .'
else ifeq ($(TARGET_OS),windows)
	$(call docker,amd64) $(IMAGE) sh -c '\
		apt-get update -qq && \
		apt-get install -y -qq --no-install-recommends gcc-mingw-w64-x86-64-win32 >/dev/null && \
		test "$$(dpkg-query -W mingw-w64-x86-64-dev | cut -f2)" = $(MINGW_VERSION) && \
		CC=x86_64-w64-mingw32-gcc-win32 CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build $(GO_BUILD) -o $(LIB) .'
else
	GOTOOLCHAIN=go$(GO_VERSION) CGO_ENABLED=1 GOOS=darwin GOARCH=$(TARGET_ARCH) \
		CGO_CFLAGS="-O2 -g $(MACOS_MIN)" CGO_LDFLAGS="$(MACOS_MIN)" go build $(GO_BUILD) -o $(LIB) .
endif

zip:
	python3 scripts/package_release.py zip $(VERSION) $(TARGET)

checksums:
	python3 scripts/package_release.py checksums $(VERSION)

# Builds every target; needs a macOS host with Docker.
release-assets:
	for target in $(TARGETS); do $(MAKE) build zip TARGET=$$target || exit 1; done
	$(MAKE) checksums

native-test:
	$(LINUX_ONLY)
	$(call docker,$(TARGET_ARCH)) $(ISOLATED) -e SSL_CERT_FILE=/tmp/quota-router-fixture.pem $(IMAGE) python3 tests/native_smoke.py $(LIB)

host-test: cpa
	$(LINUX_ONLY)
	$(call docker,$(TARGET_ARCH)) --cpus 1 --memory 384m --memory-swap 384m $(ISOLATED) $(IMAGE) python3 tests/host_smoke.py dist/cpa/$(CPA_ASSET) dist/cpa/checksums.txt $(LIB)

# Runs on this machine, so TARGET must match it.
load-test: cpa
	python3 tests/load_smoke.py dist/cpa/$(CPA_ASSET) dist/cpa/checksums.txt $(ZIP)

cpa:
	mkdir -p dist/cpa
	test -f dist/cpa/checksums.txt || curl -fsSL -o dist/cpa/checksums.txt $(CPA_URL)/checksums.txt
	test -f dist/cpa/$(CPA_ASSET) || curl -fsSL -o dist/cpa/$(CPA_ASSET) $(CPA_URL)/$(CPA_ASSET)

version:
	@echo $(VERSION)

clean:
	rm -rf dist coverage.out

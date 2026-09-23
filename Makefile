IMAGE := golang:1.26-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d
VERSION := $(shell sed -n 's/^const pluginVersion = "\(.*\)"/\1/p' config.go)
DOCKER := docker run --rm --platform linux/amd64 -v "$(CURDIR):/src" -v quota-reset-router-go-mod:/go/pkg/mod -v quota-reset-router-go-build:/root/.cache/go-build -w /src

.PHONY: test linux-test linux-build native-test host-test package clean

test:
	go test -race -count=1 ./...
	go vet ./...

linux-test:
	$(DOCKER) $(IMAGE) sh -c 'go test -race -count=1 ./... && go vet ./...'

linux-build:
	mkdir -p dist
	$(DOCKER) $(IMAGE) sh -c 'CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -ldflags="-s -w" -o dist/quota-reset-router.so . && sha256sum dist/quota-reset-router.so > dist/SHA256SUMS'

native-test:
	$(DOCKER) --network none --add-host api.anthropic.com:127.0.0.1 --add-host chatgpt.com:127.0.0.1 -e PLUGIN_ISOLATED_TEST=1 -e SSL_CERT_FILE=/tmp/quota-router-fixture.pem $(IMAGE) python3 tests/native_smoke.py dist/quota-reset-router.so

host-test:
	$(DOCKER) --cpus 1 --memory 384m --memory-swap 384m --network none --add-host api.anthropic.com:127.0.0.1 --add-host chatgpt.com:127.0.0.1 -e PLUGIN_ISOLATED_TEST=1 $(IMAGE) python3 tests/host_smoke.py dist

package:
	$(DOCKER) $(IMAGE) tar --owner=0 --group=0 --numeric-owner -czf dist/quota-reset-router_$(VERSION)_linux_amd64.tar.gz dist/quota-reset-router.so dist/SHA256SUMS README.md LICENSE

clean:
	rm -rf dist coverage.out

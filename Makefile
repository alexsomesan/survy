# survy build and deploy. See docs/operations.md.
#
#   make test             Go vet + tests, deploy script tests, shellcheck (if installed)
#   make web              build the frontend into web/dist
#   make build            web, then the static binary bin/survy
#   make image            dist/survy.tar: image localhost/dash:<sha> and :latest
#                         (docker if available, else daemonless tools/mkimage)
#   make load             image, then load it into podman on the router (no restart)
#   make deploy           load, then restart the container on the router
#   make install-scripts  copy the commit hook, boot mount and ASN fetcher to the router
#   make fetch-asn        download the ASN database into ./data (local development)

ROUTER ?= vyos
IMAGE  ?= localhost/dash
SHA    := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
SCRIPTS := deploy/partition-p5.sh deploy/preconfig-mount.sh deploy/fetch-asn.sh \
           deploy/test/lib.sh $(wildcard deploy/test/*.test.sh)

.PHONY: test test-go test-deploy web build image load deploy install-scripts fetch-asn

test: test-go test-deploy

test-go:
	go vet ./...
	go test ./...

test-deploy:
	@for t in deploy/test/*.test.sh; do echo "== $$t"; bash "$$t" || exit 1; done
	@for s in $(SCRIPTS); do bash -n "$$s" || exit 1; done
	sh -n deploy/preconfig-mount.sh
	python3 -c 'import ast,sys; ast.parse(open(sys.argv[1]).read())' deploy/commit-hook/dash-inventory
	@if command -v shellcheck >/dev/null; then shellcheck -x $(SCRIPTS); \
	 else echo "shellcheck not installed; skipped"; fi

web:
	cd web && npm ci && npm run build

# web/dist is embedded (web/embed.go), so the frontend is built first.
build: web
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/survy ./cmd/survy

# dist/survy.tar is a docker-archive with both tags. With a usable docker
# daemon it comes from the Dockerfile; without one, tools/mkimage assembles the
# same image (busybox base + the static binary) with no daemon and no root.
image:
	mkdir -p dist
	if docker info >/dev/null 2>&1; then \
	  docker build -t $(IMAGE):$(SHA) -t $(IMAGE):latest . && \
	  docker save $(IMAGE):$(SHA) $(IMAGE):latest -o dist/survy.tar; \
	else \
	  $(MAKE) build && \
	  (cd tools/mkimage && go run . -bin ../../bin/survy -o ../../dist/survy.tar \
	    -t $(IMAGE):$(SHA) -t $(IMAGE):latest); \
	fi

# podman on the router loads the docker-archive, keeping both tags. VyOS
# starts the container from localhost/dash:latest.
load: image
	ssh $(ROUTER) 'sudo podman load' < dist/survy.tar

# `restart container` (VyOS op mode) restarts the container service, which
# starts it from the configured tag localhost/dash:latest that `load` has just
# moved. docs/operations.md ("Verifying") shows how to confirm it runs the new
# image ID; not yet confirmed on this router.
deploy: load
	ssh $(ROUTER) '/opt/vyatta/bin/vyatta-op-cmd-wrapper restart container dash'
	@echo "Deployed $(IMAGE):$(SHA). Check it: docs/operations.md, 'Verifying'."

install-scripts:
	scp deploy/commit-hook/dash-inventory deploy/preconfig-mount.sh deploy/fetch-asn.sh $(ROUTER):/tmp/
	ssh $(ROUTER) 'set -e; \
	  sudo install -d -m 0755 /config/scripts/dash /config/scripts/commit/post-hooks.d; \
	  sudo install -m 0755 /tmp/dash-inventory /config/scripts/commit/post-hooks.d/dash-inventory; \
	  sudo install -m 0755 /tmp/preconfig-mount.sh /tmp/fetch-asn.sh /config/scripts/dash/; \
	  rm -f /tmp/dash-inventory /tmp/preconfig-mount.sh /tmp/fetch-asn.sh'

fetch-asn:
	mkdir -p data
	deploy/fetch-asn.sh data

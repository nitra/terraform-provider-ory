HOSTNAME=registry.opentofu.org
NAMESPACE=nitra
NAME=ory
BINARY=terraform-provider-${NAME}
# Placeholder version for local `make install` testing via a filesystem
# mirror; real releases are cut by goreleaser off a git tag.
VERSION=99.0.0
OS_ARCH=$(shell go env GOOS)_$(shell go env GOARCH)

# Container runtime for the local Hydra. Podman is the default; Docker works
# too: `make hydra-up COMPOSE="docker compose"`.
COMPOSE ?= podman compose
HYDRA_ADMIN_URL ?= http://127.0.0.1:4445
HYDRA_PUBLIC_URL ?= http://127.0.0.1:4444
# OpenTofu is used for acceptance tests when found on PATH.
TOFU := $(shell command -v tofu 2>/dev/null)
ACC_ENV = TF_ACC=1 HYDRA_ADMIN_URL=$(HYDRA_ADMIN_URL) HYDRA_PUBLIC_URL=$(HYDRA_PUBLIC_URL) \
	$(if $(TOFU),TF_ACC_TERRAFORM_PATH=$(TOFU) TF_ACC_PROVIDER_HOST=registry.opentofu.org)

.PHONY: default build install test testacc empirical hydra-up hydra-down lint docs

default: build

build:
	go build -o ${BINARY}

install: build
	mkdir -p ~/.terraform.d/plugins/${HOSTNAME}/${NAMESPACE}/${NAME}/${VERSION}/${OS_ARCH}
	mv ${BINARY} ~/.terraform.d/plugins/${HOSTNAME}/${NAMESPACE}/${NAME}/${VERSION}/${OS_ARCH}/${BINARY}_v${VERSION}

test:
	go test ./... $(TESTARGS) -timeout=60s

hydra-up:
	$(COMPOSE) up -d --wait

hydra-down:
	$(COMPOSE) down -v

# Acceptance tests + empirical Hydra checks against the local Hydra
# (run `make hydra-up` first, `make hydra-down` afterwards).
testacc:
	$(ACC_ENV) go test ./... -v -count=1 $(TESTARGS) -timeout 20m

empirical:
	HYDRA_EMPIRICAL=1 HYDRA_ADMIN_URL=$(HYDRA_ADMIN_URL) HYDRA_PUBLIC_URL=$(HYDRA_PUBLIC_URL) \
		go test ./internal/empirical -v -count=1

lint:
	test -z "$$(gofmt -l . | grep -v '^internal/httpclient/')"
	go vet ./...

docs:
	go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.25.0 generate --provider-name hydra

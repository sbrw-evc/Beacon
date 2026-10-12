.PHONY: build test run dev-api dev-web image openbao-dev

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null)
BUILT_AT ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.builtAt=$(BUILT_AT)

build:
	cd web && npm ci && npm run build
	cd backend && go build -ldflags "$(LDFLAGS)" -o umbrella ./cmd/umbrella

test:
	cd backend && go vet ./... && go test ./...
	cd web && npm run typecheck

run: build
	cd backend && ./umbrella -web ../web/dist

dev-api:
	cd backend && go run ./cmd/umbrella

dev-web:
	cd web && npm run dev

image:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILT_AT=$(BUILT_AT) -t umbrella-monitoring:$(VERSION) -t umbrella-monitoring:latest .

openbao-dev:
	docker run -d --rm --name umbrella-openbao-dev -p 127.0.0.1:8200:8200 -e BAO_DEV_ROOT_TOKEN_ID=dev-root openbao/openbao:2.7.1 server -dev -dev-listen-address=0.0.0.0:8200
	sleep 2
	docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=dev-root umbrella-openbao-dev bao secrets enable -path=umbrella -version=2 kv
	@echo 'OpenBao dev is up. In the setup wizard enter: address http://127.0.0.1:8200, token dev-root, KV v2 mount umbrella.'

BIN := bin/zentao
VERSION ?= $(shell git describe --tags --always)
DIST := dist

.PHONY: build vet test test-unit test-contract test-e2e regression testenv-up testenv-down release install-skill

build:
	go build -o $(BIN) ./cmd/zentao

vet:
	go vet ./...

# Ring 1: unit tests, no server needed, <1s.
test-unit: vet
	go test ./internal/... -count=1

test: test-unit

# Ring 2: contract tests - pin server behavior and known drift.
# Ring 3: e2e tests - the compiled binary against the server.
# Both need the test environment (see testenv-up).
test-contract:
	go test ./contract/ -v -count=1

test-e2e:
	go test ./e2e/ -v -count=1

# Full regression: everything that must stay green before shipping a change.
regression: vet build test-unit test-contract test-e2e
	@echo "REGRESSION OK"

# Cross-compiled release tarballs for the homebrew formula (and GitHub releases).
release: vet test-unit
	rm -rf $(DIST) && mkdir -p $(DIST)
	@for target in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64; do \
		os=$${target%/*}; arch=$${target#*/}; \
		echo "[build] $$os/$$arch"; \
		out=$$(mktemp -d); \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath \
			-ldflags "-s -w -X github.com/rioliu/zentao-cli-go/internal/cmd.Version=$(VERSION:v%=%)" \
			-o $$out/zentao ./cmd/zentao; \
		tar -czf $(DIST)/zentao-cli-go_$(VERSION)_$${os}_$${arch}.tar.gz -C $$out zentao; \
		rm -rf $$out; \
	done
	@ls -la $(DIST)
	@shasum -a 256 $(DIST)/*.tar.gz

# Install the bundled skill into a coding agent (pi, claude, or portable agents dir).
install-skill: build
	$(BIN) add-skill pi

# Bring up a disposable Zentao matching production (22.4) and print env exports.
testenv-up:
	ADMIN_PASSWORD=$${ADMIN_PASSWORD:?set ADMIN_PASSWORD} bash contract/testenv/up.sh
	@echo "export ZENTAO_TEST_URL=http://localhost:8088"
	@echo "export ZENTAO_TEST_ACCOUNT=admin"
	@echo "export ZENTAO_TEST_PASSWORD=\$$ADMIN_PASSWORD"

testenv-down:
	-podman rm -f zentao-testenv
	-podman volume rm zentao-testenv-data

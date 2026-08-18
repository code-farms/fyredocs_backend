SERVICES = shared api-gateway auth-service job-service \
           convert-to-pdf convert-from-pdf organize-pdf optimize-pdf \
           analytics-service document-service user-service notification-service

# Compose always uses the deployment compose file + the single root .env (same
# env file deploy.sh loads). Run one (or more) services without a long command:
#   make up SVC=auth-service                # start a service + its deps
#   make up SVC="auth-service job-service"  # multiple
#   make down SVC=auth-service              # stop just that service
#   make logs SVC=auth-service              # follow its logs
#   make ps                                 # whole-stack status
# Omit SVC to act on the whole stack.
COMPOSE = docker compose -f deployment/docker-compose.yml --env-file .env

# Dev stack: an isolated second copy of everything. The -p project name is the
# isolation mechanism — named volumes become fyredocs-dev_* so feature-branch
# migrations never touch the main stack's database/objects. The dev override
# remaps published ports so both stacks can run simultaneously
# (Caddy 8080/8443, MinIO console 9101, Loki 3101, Prometheus 9190, Grafana 3001).
DEV_COMPOSE = docker compose -p fyredocs-dev \
  -f deployment/docker-compose.yml -f deployment/docker-compose.dev.yml \
  --env-file .env

# Infra-only stack for integration-tagged tests (Postgres/Redis/NATS/MinIO on
# loopback ports). Kept separate from the full COMPOSE stack so test infra can
# run without the services themselves competing for the same containers.
ESSENTIALS_COMPOSE = docker compose -f deployment/docker-compose.essentials.yml --env-file .env

# Flags for every unit-test invocation. -count=1 defeats the build cache so a
# rerun actually executes (a cached "ok" hides newly introduced flakes);
# -shuffle=on surfaces test-order dependencies before they calcify; the
# timeout is a backstop against hangs, sized for the slowest module
# (organize-pdf's pdfcpu suites) under -race overhead.
TESTFLAGS = -race -count=1 -shuffle=on -timeout=10m

.PHONY: up down logs ps check-ports test test-v test-integration test-infra-up test-infra-down test-binaries cover bench fuzz-short lint dev-up dev-down dev-logs dev-ps dev-nuke $(addprefix test-,$(SERVICES)) $(addprefix test-v-,$(SERVICES))

## Start service(s) (SVC=…) or the whole stack; env comes from the root .env
up:
	$(COMPOSE) up -d $(SVC)

## Stop service(s) (SVC=…) or the whole stack (containers only; volumes kept)
down:
	@if [ -n "$(SVC)" ]; then $(COMPOSE) stop $(SVC); else $(COMPOSE) down --remove-orphans; fi

## Follow logs for service(s) (SVC=…) or the whole stack
logs:
	$(COMPOSE) logs -f $(SVC)

## Show container status
ps:
	$(COMPOSE) ps

## Start the ISOLATED dev stack (own volumes: fyredocs-dev_*) — safe for branch migrations
dev-up:
	$(DEV_COMPOSE) up -d --build $(SVC)

## Stop the dev stack (volumes kept — dev DB state survives)
dev-down:
	@if [ -n "$(SVC)" ]; then $(DEV_COMPOSE) stop $(SVC); else $(DEV_COMPOSE) down --remove-orphans; fi

## Follow dev-stack logs (SVC=… optional)
dev-logs:
	$(DEV_COMPOSE) logs -f $(SVC)

## Dev-stack container status
dev-ps:
	$(DEV_COMPOSE) ps

## DELETE the dev stack INCLUDING its volumes (fresh dev DB next dev-up); main stack untouched
dev-nuke:
	$(DEV_COMPOSE) down --remove-orphans -v

## Fail if any compose file host-publishes a port other than Caddy 80/443 or 127.0.0.1-bound
check-ports:
	@sh deployment/scripts/check-port-exposure.sh

## Run all tests across every service
test:
	@fail=0; \
	for svc in $(SERVICES); do \
		echo "--- $$svc ---"; \
		go test $(TESTFLAGS) ./$$svc/... || fail=1; \
	done; \
	exit $$fail

## Run all tests with verbose output
test-v:
	@fail=0; \
	for svc in $(SERVICES); do \
		echo "--- $$svc ---"; \
		go test -v $(TESTFLAGS) ./$$svc/... || fail=1; \
	done; \
	exit $$fail

## Run tests for a single service  (e.g. make test-shared, make test-api-gateway)
$(addprefix test-,$(SERVICES)):
	go test $(TESTFLAGS) ./$(@:test-%=%)/...

## Verbose variant  (e.g. make test-v-shared, make test-v-api-gateway)
$(addprefix test-v-,$(SERVICES)):
	go test -v $(TESTFLAGS) ./$(@:test-v-%=%)/...

## Start ONLY the infra containers integration tests need (loopback ports)
test-infra-up:
	$(ESSENTIALS_COMPOSE) up -d db redis nats minio minio-init

## Stop the integration-test infra containers (volumes kept)
test-infra-down:
	$(ESSENTIALS_COMPOSE) down --remove-orphans

## Run integration-tagged tests against the essentials stack (make test-infra-up first).
# -p 1 because integration packages share the test database; parallel packages
# would TRUNCATE each other's rows mid-test. .env is sourced (set -a, the same
# way deploy.sh loads it) so shared/database/dbtest derives the DSN from the
# stack's real POSTGRES_* credentials.
test-integration:
	@set -a; [ -f .env ] && . ./.env; set +a; \
	fail=0; \
	for svc in $(SERVICES); do \
		echo "--- $$svc (integration) ---"; \
		go test -tags=integration $(TESTFLAGS) -p 1 ./$$svc/... || fail=1; \
	done; \
	exit $$fail

## Run binaries-tagged tests (real gs/libreoffice/tesseract exec paths) in Docker.
# The suite is file-in/file-out (no infra); the container just supplies the
# tools the host lacks. Module/build caches are volume-mounted so reruns are
# incremental. Scope with SVC=<module> (default: the four PDF workers).
BINARIES_SVCS ?= convert-to-pdf convert-from-pdf organize-pdf optimize-pdf
test-binaries:
	docker build -q -f deployment/test-binaries.Dockerfile -t fyredocs-test-binaries deployment >/dev/null
	docker run --rm -v "$(CURDIR)":/src -v fyredocs-test-gomod:/go/pkg/mod \
		-v fyredocs-test-gocache:/root/.cache/go-build \
		fyredocs-test-binaries bash -c '\
		fail=0; \
		for svc in $(or $(SVC),$(BINARIES_SVCS)); do \
			echo "--- $$svc (binaries) ---"; \
			go test -tags="integration binaries" -count=1 -timeout=15m ./$$svc/... || fail=1; \
		done; \
		exit $$fail'

## Per-module coverage profiles merged into coverage/merged.out + func summary
cover:
	@bash scripts/coverage.sh

## Run all benchmarks (no tests: -run=^$$ keeps the loop pure benchmark time)
bench:
	@fail=0; \
	for svc in $(SERVICES); do \
		echo "--- $$svc (bench) ---"; \
		go test -bench=. -benchmem -run='^$$' ./$$svc/... || fail=1; \
	done; \
	exit $$fail

## Run every fuzz target briefly (smoke fuzzing; long sessions are manual)
fuzz-short:
	@bash scripts/fuzz-short.sh

## Format check + vet across every module (golangci-lint added on top if installed)
lint:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed on:"; echo "$$unformatted"; exit 1; \
	fi
	@fail=0; \
	for svc in $(SERVICES); do \
		echo "--- $$svc (vet) ---"; \
		go vet ./$$svc/... || fail=1; \
	done; \
	exit $$fail

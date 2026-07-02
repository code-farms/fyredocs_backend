SERVICES = shared api-gateway auth-service job-service \
           convert-to-pdf convert-from-pdf organize-pdf optimize-pdf

# Compose always uses the deployment compose file + the single root .env (same
# env file deploy.sh loads). Run one (or more) services without a long command:
#   make up SVC=auth-service                # start a service + its deps
#   make up SVC="auth-service job-service"  # multiple
#   make down SVC=auth-service              # stop just that service
#   make logs SVC=auth-service              # follow its logs
#   make ps                                 # whole-stack status
# Omit SVC to act on the whole stack.
COMPOSE = docker compose -f deployment/docker-compose.yml --env-file .env

.PHONY: up down logs ps test test-v $(addprefix test-,$(SERVICES)) $(addprefix test-v-,$(SERVICES))

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

## Run all tests across every service
test:
	@fail=0; \
	for svc in $(SERVICES); do \
		echo "--- $$svc ---"; \
		go test ./$$svc/... || fail=1; \
	done; \
	exit $$fail

## Run all tests with verbose output
test-v:
	@fail=0; \
	for svc in $(SERVICES); do \
		echo "--- $$svc ---"; \
		go test -v ./$$svc/... || fail=1; \
	done; \
	exit $$fail

## Run tests for a single service  (e.g. make test-shared, make test-api-gateway)
$(addprefix test-,$(SERVICES)):
	go test ./$(@:test-%=%)/...

## Verbose variant  (e.g. make test-v-shared, make test-v-api-gateway)
$(addprefix test-v-,$(SERVICES)):
	go test -v ./$(@:test-v-%=%)/...

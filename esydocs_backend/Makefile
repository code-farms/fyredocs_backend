SERVICES = shared api-gateway auth-service job-service \
           convert-to-pdf convert-from-pdf organize-pdf optimize-pdf \
           cleanup-worker

.PHONY: test test-v $(addprefix test-,$(SERVICES)) $(addprefix test-v-,$(SERVICES))

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

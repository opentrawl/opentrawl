.PHONY: test
test:
	GOWORK=off go test ./...

.PHONY: verify
verify:
	GOWORK=off go mod tidy
	GOWORK=off go test ./...

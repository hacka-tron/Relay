.PHONY: test check fmt

test:
	go test ./...

check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)
	go vet ./...
	go build ./...

fmt:
	gofmt -w .

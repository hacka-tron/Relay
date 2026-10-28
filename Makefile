.PHONY: test check fmt demo keygen build server

test:
	go test ./...

check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)
	go vet ./...
	go build ./...

fmt:
	gofmt -w .

demo:
	./scripts/demo.sh

keygen:
	go run ./cmd/release keygen

# make build VERSION=1.2.0
build:
	go run ./cmd/release build --version $(VERSION)

server:
	go run ./cmd/updateserver

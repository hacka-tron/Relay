.PHONY: test check fmt demo next bad keygen build fetch server

test:
	go test ./...

check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)
	go vet ./...
	go build ./...

fmt:
	gofmt -w .

# The local demo: start it, then publish versions from another terminal.
demo:
	go run ./cmd/demo start

next:
	go run ./cmd/demo next

bad:
	go run ./cmd/demo bad

keygen:
	go run ./cmd/release keygen

# make build VERSION=1.2.0
build:
	go run ./cmd/release build --version $(VERSION)

# make fetch VERSION=1.2.0: pull a CI-built release into releases/ so the
# local server can offer it.
fetch:
	gh release download v$(VERSION) --dir releases/$(VERSION) --clobber

server:
	go run ./cmd/updateserver

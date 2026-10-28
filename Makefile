.PHONY: test check fmt demo keygen build fetch server

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

# make fetch VERSION=1.2.0: pull a CI-built release into releases/ so the
# local server can offer it.
fetch:
	gh release download v$(VERSION) --dir releases/$(VERSION) --clobber

server:
	go run ./cmd/updateserver

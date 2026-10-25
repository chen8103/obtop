GO ?= go
export GO

.PHONY: build test vet race release clean
build:
	@sh scripts/build.sh build

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

race:
	CGO_ENABLED=1 $(GO) test -race ./...

release:
	@sh scripts/build.sh release

clean:
	@sh scripts/build.sh clean

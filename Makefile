STATICCHECK_VERSION := 2026.1

.PHONY: build fmt run staticcheck test

build:
	go build -o bin/stacks ./cmd/stacks

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

run:
	go run ./cmd/stacks

test:
	go test ./...

staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

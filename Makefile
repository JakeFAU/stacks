.PHONY: build fmt run test vet

build:
	go build -o bin/stacks ./cmd/stacks

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

run:
	go run ./cmd/stacks

test:
	go test ./...

vet:
	go vet ./...


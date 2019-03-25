build:
	hack/build-go.sh
.PHONY: build

test:
	go test ./...
.PHONY: test

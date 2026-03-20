.PHONY: test test-race test-bench fmt vet cover tidy

test:
	go test ./...

test-race:
	go test -race ./...

test-bench:
	go test -bench=. ./...

fmt:
	gofmt -w .
	goimports -w .

vet:
	go vet ./...

cover:
	go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out

tidy:
	go mod tidy

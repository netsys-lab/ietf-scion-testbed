BIN := bin

.PHONY: build test deb clean

build:
	mkdir -p $(BIN)
	CGO_ENABLED=0 go build -ldflags "-s -w" -o $(BIN)/hev3 ./cmd/hev3
	# cmd/hev3-server: demo target server, deferred to Task 11 (not yet present).
	# CGO_ENABLED=0 go build -ldflags "-s -w" -o $(BIN)/hev3-server ./cmd/hev3-server

test:
	go test ./...

# deb: packaging target, deferred to Task 11

clean:
	rm -rf bin dist

VERSION ?= 0.1.0
BIN := bin
STAGE := dist/stage

.PHONY: build test deb clean

build:
	mkdir -p $(BIN)
	CGO_ENABLED=0 go build -ldflags "-s -w" -o $(BIN)/hev3 ./cmd/hev3
	CGO_ENABLED=0 go build -ldflags "-s -w" -o $(BIN)/hev3-server ./cmd/hev3-server

test:
	go test ./...

# deb: builds scion-hev3, shipping both the hev3 CLI and the hev3-server demo
# target plus the testbed CA cert at /etc/hev3/ca.pem (a conffile). No systemd
# unit: unlike linkd, hev3-server runs on only two hosts, so ansible templates
# its unit + per-host cert/key. The same deb goes to CLI-only hosts (they carry
# an unused hev3-server binary — acceptable, keeps one artifact for every host).
deb: build
	rm -rf $(STAGE)
	mkdir -p $(STAGE)/DEBIAN $(STAGE)/usr/local/bin $(STAGE)/etc/hev3
	install -m 0755 $(BIN)/hev3 $(STAGE)/usr/local/bin/hev3
	install -m 0755 $(BIN)/hev3-server $(STAGE)/usr/local/bin/hev3-server
	install -m 0644 ../ansible/files/hev3-ca/ca.pem $(STAGE)/etc/hev3/ca.pem
	printf 'Package: scion-hev3\nVersion: $(VERSION)\nArchitecture: amd64\nMaintainer: netsys-lab\nDescription: SCION-aware Happy Eyeballs v3 CLI + demo target server\n hev3 races SCION vs IPv6/IPv4 per draft-ietf-happy-happyeyeballs-v3;\n hev3-server is the dual-stack (h2/h1.1/h3/SCION-h3) demo target.\n' > $(STAGE)/DEBIAN/control
	printf '/etc/hev3/ca.pem\n' > $(STAGE)/DEBIAN/conffiles
	mkdir -p dist
	dpkg-deb --build --root-owner-group $(STAGE) dist/scion-hev3_$(VERSION)_amd64.deb

clean:
	rm -rf bin dist

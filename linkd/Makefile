VERSION ?= 0.3.2
BIN := bin/scion-linkd
STAGE := dist/stage

.PHONY: build test deb clean

build:
	CGO_ENABLED=0 go build -ldflags "-s -w" -o $(BIN) ./cmd/scion-linkd

test:
	go test ./...

deb: build
	rm -rf $(STAGE)
	mkdir -p $(STAGE)/DEBIAN $(STAGE)/usr/bin $(STAGE)/lib/systemd/system $(STAGE)/etc/scion-linkd
	cp $(BIN) $(STAGE)/usr/bin/
	cp pkg/scion-linkd.service $(STAGE)/lib/systemd/system/
	cp pkg/config.toml $(STAGE)/etc/scion-linkd/
	cp pkg/postinst pkg/prerm $(STAGE)/DEBIAN/
	chmod 755 $(STAGE)/DEBIAN/postinst $(STAGE)/DEBIAN/prerm
	printf 'Package: scion-linkd\nVersion: $(VERSION)\nArchitecture: amd64\nMaintainer: netsys-lab\nDepends: systemd\nDescription: SCION testbed link shaping daemon\n conffiles-managed netem control for inter-AS links.\n' > $(STAGE)/DEBIAN/control
	printf '/etc/scion-linkd/config.toml\n' > $(STAGE)/DEBIAN/conffiles
	dpkg-deb --build --root-owner-group $(STAGE) dist/scion-linkd_$(VERSION)_amd64.deb

clean:
	rm -rf bin dist

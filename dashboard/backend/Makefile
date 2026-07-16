VERSION ?= 0.1.2
BIN := bin/fabricd
STAGE := dist/stage
WEB_DIST := ../web/dist

.PHONY: build test deb clean

build:
	CGO_ENABLED=0 go build -ldflags "-s -w" -o $(BIN) ./cmd/fabricd

test:
	go test ./...

# deb stages the binary, systemd unit, and default config exactly as linkd's
# Makefile does, plus the built frontend (dashboard/web/dist). The frontend
# is built in a separate step (cd ../web && npm run build); a missing
# web/dist fails the build here so we never silently ship a UI-less deb.
# Set SKIP_WEB=1 to intentionally build a headless deb (e.g. for backend-only
# dev iteration).
deb: build
	rm -rf $(STAGE)
	mkdir -p $(STAGE)/DEBIAN $(STAGE)/usr/bin $(STAGE)/lib/systemd/system $(STAGE)/etc/fabricd
	cp $(BIN) $(STAGE)/usr/bin/
	cp pkg/fabricd.service $(STAGE)/lib/systemd/system/
	cp pkg/config.toml $(STAGE)/etc/fabricd/
	if [ -d $(WEB_DIST) ]; then \
		mkdir -p $(STAGE)/usr/share/fabricd/web; \
		cp -r $(WEB_DIST)/. $(STAGE)/usr/share/fabricd/web/; \
	elif [ -n "$(SKIP_WEB)" ]; then \
		echo "make deb: $(WEB_DIST) not found, packaging without the frontend (SKIP_WEB set)"; \
	else \
		echo "make deb: $(WEB_DIST) not found."; \
		echo "  Run 'cd ../web && npm run build' first, or set SKIP_WEB=1 to build a headless deb on purpose."; \
		exit 1; \
	fi
	printf 'Package: fabricd\nVersion: $(VERSION)\nArchitecture: amd64\nMaintainer: netsys-lab\nDepends: systemd\nDescription: SCION Fabrik dashboard backend\n topology visualization + link control API for the SCION testbed.\n' > $(STAGE)/DEBIAN/control
	printf '/etc/fabricd/config.toml\n' > $(STAGE)/DEBIAN/conffiles
	dpkg-deb --build --root-owner-group $(STAGE) dist/fabricd_$(VERSION)_amd64.deb

clean:
	rm -rf bin dist

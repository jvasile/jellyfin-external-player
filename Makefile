PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
MANDIR ?= $(PREFIX)/share/man/man1

VERSION := 0.1.0
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILDTIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.Version=$(VERSION) -X main.CommitHash=$(COMMIT) -X main.BuildTime=$(BUILDTIME)

.PHONY: all linux windows installer install install-service deb github-release clean

all: linux windows

linux: jellyfin-external-player

windows: jellyfin-external-player.exe

jellyfin-external-player: cmd/jellyfin-external-player/*.go go.mod
	go build -ldflags "$(LDFLAGS)" -o jellyfin-external-player ./cmd/jellyfin-external-player

jellyfin-external-player.exe: cmd/jellyfin-external-player/*.go go.mod
	GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui $(LDFLAGS)" -o jellyfin-external-player.exe ./cmd/jellyfin-external-player

# Build Windows installer using NSIS
# Install: sudo apt install nsis
installer: jellyfin-external-player.exe jellyfin-external-player.js
	makensis installer.nsi
	chmod +x jellyfin-external-player-setup.exe

install: linux
	install -d $(DESTDIR)$(BINDIR)
	install -d $(DESTDIR)$(MANDIR)
	install -m 755 jellyfin-external-player $(DESTDIR)$(BINDIR)/
	install -m 644 jellyfin-external-player.js $(DESTDIR)$(BINDIR)/
	install -m 644 jellyfin-external-player.1 $(DESTDIR)$(MANDIR)/

install-service:
	mkdir -p ~/.config/systemd/user
	cp jellyfin-external-player.service ~/.config/systemd/user/
	systemctl --user daemon-reload
	systemctl --user enable jellyfin-external-player

DEB_VERSION := $(shell dpkg-parsechangelog -S Version)

deb:
	@if [ "$(VERSION)" != "$(DEB_VERSION)" ]; then \
		echo "Error: Makefile VERSION ($(VERSION)) != debian/changelog version ($(DEB_VERSION))"; \
		exit 1; \
	fi
	dpkg-buildpackage

github-release: deb installer
	gh release create v$(VERSION) \
		../jellyfin-external-player_$(VERSION)_amd64.deb \
		jellyfin-external-player-setup.exe \
		--title "v$(VERSION)" \
		--notes "Release v$(VERSION)"

clean:
	rm -f jellyfin-external-player jellyfin-external-player.exe jellyfin-external-player-setup.exe

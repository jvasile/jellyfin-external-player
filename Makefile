PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
MANDIR ?= $(PREFIX)/share/man/man1

VERSION := 0.2.0
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILDTIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.Version=$(VERSION) -X main.CommitHash=$(COMMIT) -X main.BuildTime=$(BUILDTIME)

.PHONY: all linux windows windows-installer install install-service deb github-release clean

all: linux windows

linux: jellyfin-external-player

windows: windows/jellyfin-external-player.exe

jellyfin-external-player: cmd/jellyfin-external-player/*.go go.mod
	go build -ldflags "$(LDFLAGS)" -o jellyfin-external-player ./cmd/jellyfin-external-player

windows/jellyfin-external-player.exe: cmd/jellyfin-external-player/*.go go.mod
	GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui $(LDFLAGS)" -o windows/jellyfin-external-player.exe ./cmd/jellyfin-external-player

# Build Windows installer using NSIS
# Install: sudo apt install nsis
windows-installer: windows/jellyfin-external-player-setup.exe

windows/jellyfin-external-player-setup.exe: windows/jellyfin-external-player.exe dist/jellyfin-external-player.js windows/installer.nsi
	cd windows && makensis installer.nsi
	chmod +x windows/jellyfin-external-player-setup.exe

install: linux
	install -d $(DESTDIR)$(BINDIR)
	install -d $(DESTDIR)$(MANDIR)
	install -m 755 jellyfin-external-player $(DESTDIR)$(BINDIR)/
	install -m 644 dist/jellyfin-external-player.js $(DESTDIR)$(BINDIR)/
	install -m 644 dist/jellyfin-external-player.1 $(DESTDIR)$(MANDIR)/

install-service:
	mkdir -p ~/.config/systemd/user
	cp dist/jellyfin-external-player.service ~/.config/systemd/user/
	systemctl --user daemon-reload
	systemctl --user enable jellyfin-external-player

DEB_VERSION := $(shell dpkg-parsechangelog -S Version)

# DEB_SIGN: unset = use default key, "no" = unsigned, <keyid> = specific key
ifdef DEB_SIGN
ifeq ($(DEB_SIGN),no)
DEB_SIGN_FLAGS := -us -uc
else
DEB_SIGN_FLAGS := -k$(DEB_SIGN)
endif
endif

deb:
	@if [ "$(VERSION)" != "$(DEB_VERSION)" ]; then \
		echo "Error: Makefile VERSION ($(VERSION)) != debian/changelog version ($(DEB_VERSION))"; \
		exit 1; \
	fi
	dpkg-buildpackage $(DEB_SIGN_FLAGS)

github-release: deb windows-installer
	gh release create v$(VERSION) \
		../jellyfin-external-player_$(VERSION)_amd64.deb \
		windows/jellyfin-external-player-setup.exe \
		--title "v$(VERSION)" \
		--notes "Release v$(VERSION)"

clean:
	rm -f jellyfin-external-player windows/jellyfin-external-player.exe windows/jellyfin-external-player-setup.exe

PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
MANDIR ?= $(PREFIX)/share/man/man1

.PHONY: all linux windows installer install install-service vendor deb clean

all: linux windows

linux: jellyfin-external-player

windows: jellyfin-external-player.exe

jellyfin-external-player: cmd/jellyfin-external-player/*.go go.mod
	go build -o jellyfin-external-player ./cmd/jellyfin-external-player

jellyfin-external-player.exe: cmd/jellyfin-external-player/*.go go.mod
	GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui" -o jellyfin-external-player.exe ./cmd/jellyfin-external-player

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

vendor:
	go mod tidy
	go mod vendor

deb:
	dpkg-buildpackage -us -uc

clean:
	rm -f jellyfin-external-player jellyfin-external-player.exe jellyfin-external-player-setup.exe

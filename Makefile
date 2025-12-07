PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
MANDIR ?= $(PREFIX)/share/man/man1

.PHONY: build install install-service vendor deb clean

build:
	go build -mod=vendor -o jellyfin-external-player .

install: build
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
	rm -f jellyfin-external-player

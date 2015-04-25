doc.md: *.go
	go get github.com/robertkrimen/godocdown/godocdown
	$(GOPATH)/bin/godocdown >doc.md.tmp
	mv doc.md.tmp doc.md
minor:=0.1
commitdate:=$(shell git log --first-parent --max-count=1 --format=format:%ci | tr -d - | head -c8)
commitabbrev:=$(shell git log --first-parent --max-count=1 --format=format:%h)
.PHONY: dist
dist:
	mkdir -p dist
	go build .
	for type in deb rpm tar; do fpm -t $$type -s dir -n streamserve -v $(minor).$(commitdate) --iteration $(commitabbrev) --prefix /usr/bin streamserve; done
	mv *.deb *.rpm dist
	mv streamserve.tar dist/streamserve-$(minor).$(commitdate)-$(commitabbrev).tar
	bzip2 -v dist/*.tar

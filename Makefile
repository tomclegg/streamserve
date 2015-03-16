doc.md: *.go
	go get github.com/robertkrimen/godocdown/godocdown
	$(GOPATH)/bin/godocdown >doc.md.tmp
	mv doc.md.tmp doc.md
.PHONY: dist
dist:
	mkdir -p dist
	go build .
	for type in deb rpm tar; do fpm -t $$type -s dir -n streamserve -v 0.1.$(shell git log --first-parent --max-count=1 --format=format:%ci | tr -d - | head -c8) --iteration $(shell git log --first-parent --max-count=1 --format=format:%h) --prefix /usr/bin streamserve; done
	mv *.deb *.rpm *.tar dist
	bzip2 -v dist/*.tar

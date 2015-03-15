doc.md: *.go
	go get github.com/robertkrimen/godocdown/godocdown
	$(GOPATH)/bin/godocdown >doc.md.tmp
	mv doc.md.tmp doc.md

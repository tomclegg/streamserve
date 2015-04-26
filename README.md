# streamserve

Efficiently distribute media streams to http clients.

* Release status: alpha
* [Documentation](doc.md) (same as [at godoc.org](https://godoc.org/github.com/tomclegg/streamserve))
* [TODO](TODO.md)
* [AGPLv3](LICENSE)

Install binary package (linux amd64):
* Download a deb, rpm, or tar.bz2 from the [Releases](https://github.com/tomclegg/streamserve/releases) page
* Install it with dpkg, rpm, or tar

Install from source (assuming you have the Go tools installed and
`$GOPATH/bin` is in your `PATH`):

```
go get github.com/tomclegg/streamserve
```

Run:

```
streamserve -address :44100 \
            -source-buffer 40 \
            -frame-bytes 44100 \
            -reopen \
            -close-idle=false \
            -exec arecord -f cd --file-type raw

streamserve -address :6464 \
            -content-type audio/mpeg \
            -source-buffer 1000 \
            -frame-bytes 300 \
            -frame-filter mp3 \
            -reopen \
            -close-idle \
            -exec sh -c 'curl -sS localhost:44100 | lame -r -h -b 64 -a -m l - -'
```

Features / design goals

* Fast. Laptop should handle 1000 clients easily.
* Small. Use well under 1GB RAM when serving 10 streams to 10K clients.
* Stream sort-of-streamable formats like mp3.
* Drop frames when clients are too slow.
* Keep clients online while sources fail and resume.

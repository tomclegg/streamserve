# streamserve

Efficiently distribute media streams to http clients.

* Release status: alpha
* [Documentation](doc.md) (same as [at godoc.org](https://godoc.org/github.com/tomclegg/streamserve))
* [TODO](TODO.md)
* [AGPLv3](LICENSE)

```
streamserve -address :44100 \
            -source-buffer 40 \
            -frame-bytes 44100 \
            -reopen=false \
            -close-idle=false \
            -path <(arecord -f cd --file-type raw)
```

Features / design goals

* Fast. Laptop should handle 1000 clients easily.
* Small. Use well under 1GB RAM when serving 10 streams to 10K clients.
* Stream sort-of-streamable formats like mp3.
* Drop frames when clients are too slow.
* Keep clients online while sources fail and resume.

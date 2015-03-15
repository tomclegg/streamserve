# streamserve

Efficiently distribute media streams to http clients.

See [usage docs at godoc.org](https://godoc.org/github.com/tomclegg/streamserve).

# release status

alpha.

See [TODO](TODO.md).

# example

Record uncompressed audio. Distribute it to whoever asks.

```
arecord -f cd --file-type raw | streamserve -address=:44100 -path=/dev/stdin -source-buffer=40 -frame-bytes=44100 -reopen=false
```

# why

Features / design goals
* Fast. Laptop should handle 1000 clients easily (if the network does).
* Small. Use well under 1GB RAM when serving 10 streams to 10K clients.
* Stream sort-of-streamable formats like wav/riff (also mp3/ogg). (TODO)
* Serve multiple streams: request URI maps to a FIFO. (TODO)
* Drop frames when clients are too slow.
* Log stats about clients while they are still connected.
* Keep clients online while sources fail and resume.

# license

AGPLv3, see [LICENSE](LICENSE).

## copyright / author

Tom Clegg (details in git history).

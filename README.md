# streamserve

Efficiently distribute media streams to http clients.

# status

Just getting started.

# example

Record uncompressed audio. Distribute it to whoever asks.

```
arecord -f cd | streamserve -listen=0.0.0.0:8888 -path=/dev/stdin -source-buffer=40 -frame-bytes=44100 -header-bytes=44
```

# why

Features / design goals
* Fast. Laptop should handle 1000 clients (if the network does).
* Stream sort-of-streamable formats like wav/riff (also mp3/ogg).
* Serve multiple streams: request URI maps to a FIFO.
* Drop frames when clients are too slow.
* Log stats about clients while they are still connected.
* Keep clients online while sources fail and resume.

# license

AGPLv3

# author(s)

See git-blame

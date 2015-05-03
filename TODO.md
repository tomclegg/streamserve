# TODO

* -client-stats-log-interval (incl. show skipped frames).
* Check mp3 logical frames.
* Refactor source frame reader to use bufio.
* Multiple streams (pay attention to client URI). Caveat: can't exit-on-idle.
* Log each client's stats in LogStats().
* Test log interval feature.
* MIME types.
* Uplink.
* TLS.
* TLS client cert verificiation.
* Refactor header feature using filter context.

## Examples/utilities todo

* pcm->serve->lame->serve
* stream to on-disk buffer: `-filter=log:fmt=time:path=/dir`?
* serve intervals from on-disk buffer

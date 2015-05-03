# streamserve
--
Streamserve is a live media streaming server: it reads data from a pipe and
distributes it to many HTTP clients.


### Usage

In general:

    streamserve [options]

PCM audio:

    streamserve -address :44100 -source-buffer 40 -frame-bytes 44100 -reopen -close-idle=false \
                -exec arecord -f cd --file-type raw

MP3 audio:

    streamserve -address 127.0.0.1:8064 -source-buffer 400 -reopen -close-idle \
                -content-type audio/mpeg \
                -frame-bytes 300 \
                -frame-filter mp3 \
                -exec sh -c 'curl -sS localhost:44100 | lame -r -h -b 64 -a -m l - -'

Show all options:

    streamserve -help


### Purpose

Unlike general-purpose web servers, streamserve it suitable when:

1. The process supplying the data stream is expensive. For example, you want to
encode media on the fly and send it to 100 clients, but you can't run 100
encoding processes at once.

2. It's acceptable for a given client (a) to start receiving data mid-stream,
and (b) if it has been receiving data too slowly, to skip some data segments to
catch up with everyone else. (For this to work, the server must understand
enough about the data stream format to interrupt and resume at appropriate
positions.)

3. Clients are expected to read data at the same speed data is received from the
source.


Listening address

By default, streamserve listens for connections at port 80 on all network
interfaces.

Note: Use 'sudo setcap cap_net_bind_service=+ep [...]/bin/streamserve' if you
want to listen on port 80 or another privileged port. Don't run streamserve as
root.

Specify an IP address and port number:

    -address 10.1.2.3:10123

Specify a port number, listen on all interfaces:

    -address :1234

Choose any unused port (can be useful for testing):

    -address :0

Default:

    -address :80


### Frames

Input data is split into frames. When a client first connects, it starts
receiving data at a frame boundary. When a client is receiving data too slowly
and skips some of the stream, the skipped part starts and ends at frame
boundaries.

The raw filter chunks its input into fixed-size blocks. It pays no attention to
the data content.

    -filter raw -frame-bytes 64

The mp3 filter accepts physical MP3 frames. It skips past input data that
doesn't look like MP3 frames. The -frame-bytes argument is a maximum: it must be
big enough to hold the biggest frame.

    -filter mp3 -frame-bytes 2048


### Buffers

Data from the input FIFO is read into a fixed-size ring buffer, with a static
number of frames. The size of the buffer determines how far a client can fall
behind before it catches up by skipping part of the stream.

The buffer size is given in frames. This is a 1048576-byte buffer:

    -frame-bytes 1024 -source-buffer 1024

Maximum client lag is determined by the source buffer argument and the actual
frame sizes: If using -filter=mp3, the above example will skip frames when a
client lags behind by 1024 mp3 frames (not 1048576 bytes).


Starting and stopping

You can control streamserve's behaviour when a data source closes, and when it
becomes idle (no clients are connected).

Keep reading data even when no clients are connected:

    -close-idle=false

Close the FIFO when no clients are connected:

    -close-idle=true

If the input FIFO reaches EOF or encounters an error, reopen it and keep
reading.

    -reopen=true

If the input FIFO reaches EOF or encounters an error, disconnect all clients and
exit.

    -reopen=false


### Limits

Limit CPU usage. The default is to use as many threads as you have CPU cores.

    -cpu-max 1

Disconnect clients after a specified number of bytes.

    -client-max-bytes 1000000000

Limit how fast the input is read. (This is meant for testing. You could also use
it to serve static content from a regular file as if it were a live stream,
although a regular static file server would probably be a better choice.) Speed
is given in bytes per second. The default is to read data as fast as the input
FIFO supplies it.

    -source-bandwidth 16000

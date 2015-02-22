package main

import (
	"errors"
	"flag"
	"log"
	"runtime"
	"time"
)

type Config struct {
	Addr            string
	Path            string
	FrameBytes      uint64
	HeaderBytes     uint64
	SourceBuffer    uint64
	SourceBandwidth uint64
	ClientMaxBytes  uint64
	CloseIdle       bool
	CpuMax          int
	Reopen          bool
	StatLogInterval time.Duration
}

var config Config

func init() {
	config = Config{}
	c := &config
	flag.StringVar(&c.Addr, "address", "0.0.0.0:80",
		"Address to listen on: \"host:port\" where host and port can be names or numbers.")
	flag.StringVar(&c.Path, "path", "/dev/stdin",
		"Path to a source fifo, or a directory containing source fifos mapped onto the URI namespace.")
	flag.Uint64Var(&c.FrameBytes, "frame-bytes", 64,
		"Size of a data frame. Only complete frames are sent to clients.")
	flag.Uint64Var(&c.HeaderBytes, "header-bytes", 0,
		"Size of header. A header is read from each source when it is opened, and delivered to each client before sending any data bytes.")
	flag.Uint64Var(&c.SourceBuffer, "source-buffer", 64,
		"Number of frames to keep in memory for each source. The smaller this buffer is, the sooner a slow client will miss frames.")
	flag.Uint64Var(&c.SourceBandwidth, "source-bandwidth", 0,
		"Maximum bandwidth for each source, in bytes per second. 0=unlimited.")
	flag.Uint64Var(&c.ClientMaxBytes, "client-max-bytes", 0,
		"Maximum bytes to send to each client. 0=unlimited.")
	flag.BoolVar(&c.CloseIdle, "close-idle", false,
		"Close an input FIFO if all of its clients disconnect. This stops whatever process is writing to the FIFO, which can be useful if that process consumes resources, but depends on that process to restart/resume reliably. The FIFO will reopen next time a client requests it.")
	flag.IntVar(&c.CpuMax, "cpu-max", runtime.NumCPU(),
		"Maximum OS procs/threads to use. This effectively limits CPU consumption to the given number of cores. The default is the number of CPUs reported by the system. If 0 is given, the default is used.")
	flag.BoolVar(&c.Reopen, "reopen", true,
		"Reopen and resume reading if an error is encountered while reading an input FIFO. Default is true. Use -reopen=false to disable.")
	flag.DurationVar(&c.StatLogInterval, "stat-log-interval", 0,
		"Seconds between periodic statistics logs for each stream source, or 0 to disable.")
}

func (c Config) Check() error {
	if c.SourceBuffer <= 2 {
		return errors.New("-source-buffer must be greater than 2.")
	}
	if c.FrameBytes < 1 {
		return errors.New("-frame-bytes must not be zero.")
	}
	if c.Path == "" {
		return errors.New("-path must not be empty.")
	}
	if c.CpuMax < 0 {
		return errors.New("-cpu-max must not be negative.")
	}
	if c.CpuMax == 0 {
		c.CpuMax = runtime.NumCPU()
	}
	return nil
}

func main() {
	flag.Parse()
	if err := config.Check(); err != nil {
		log.Fatalf("Invalid configuration: %s", err)
	}
	listening := make(chan string)
	go func() { log.Printf("Listening at %s", <-listening) }()
	log.Fatal(RunNewServer(&config, listening, nil))
}

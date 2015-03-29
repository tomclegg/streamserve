package main

import (
	"bufio"
	"io"
	"log"
	"net"
	"net/http"
	"runtime"
	"sync"
	"syscall"
	"time"
)

type Server struct {
	http.Server
	Addr       string // host:port listening ("" if not listening yet)
	Err        error  // error encountered while running server (nil if still listening)
	config     Config
	listener   *net.TCPListener
	shutdown   bool // shutdown was requested by Close()
	done       bool // server is no longer listening
	*sync.Cond      // can wait for done to become true
	sourceMap  *SourceMap
}

// FlushyResponseWriter wraps http.ResponseWriter, calling Flush()
// after each Write() to minimize buffering on our (server) end of the
// connection.
type FlushyResponseWriter struct {
	http.ResponseWriter
}

// Write sends data to the client. It panics if the underlying
// ResponseWriter does not implement http.Flusher.
func (writer *FlushyResponseWriter) Write(data []byte) (int, error) {
	defer func() {
		flusher, ok := writer.ResponseWriter.(http.Flusher)
		if !ok {
			log.Fatal("ResponseWriter is not a flusher")
		}
		flusher.Flush()
	}()
	return writer.ResponseWriter.Write(data)
}

// Run starts an HTTP server with the given configuration.
func (srv *Server) Run(c *Config) (err error) {
	// c.Check() is just for tests -- main() has already done this.
	if err = c.Check(); err != nil {
		log.Fatal(err)
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(c.CPUMax))
	addr, err := net.ResolveTCPAddr("tcp", c.Addr)
	if err != nil {
		return
	}
	srv.listener, err = net.ListenTCP("tcp", addr)
	if err != nil {
		return
	}
	srv.Addr = srv.listener.Addr().String()
	srv.sourceMap = NewSourceMap()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(writer http.ResponseWriter, req *http.Request) {
		startTime := time.Now()
		sreader := srv.sourceMap.NewReader(c.Path, c)
		fwriter := &FlushyResponseWriter{writer}
		wroteBytes, err := io.Copy(fwriter,
			bufio.NewReaderSize(sreader, int(c.FrameBytes)))
		if e, ok := err.(*net.OpError); ok {
			if e, ok := e.Err.(syscall.Errno); ok {
				if e == syscall.ECONNRESET {
					// Not really an error: client disconnected.
					err = nil
				}
			}
		}
		if err != nil {
			log.Printf("client %s error: %s", req.RemoteAddr, err)
		}
		log.Println("client", req.RemoteAddr,
			"--", time.Since(startTime).String(), "elapsed",
			wroteBytes, "bytes",
			sreader.FramesRead, "frames +",
			sreader.FramesSkipped, "skipped")
		sreader.Close()
		if srv.sourceMap.Count() == 0 && !c.Reopen {
			// The only source path has ended and can't be reopened.
			srv.Close()
		}
	})
	srv.Handler = mux
	mutex := &sync.RWMutex{}
	srv.Cond = sync.NewCond(mutex.RLocker())
	go func() {
		err = srv.Serve(tcpKeepAliveListener{srv.listener})
		if !srv.shutdown {
			srv.Err = err
		}
		mutex.Lock()
		srv.done = true
		srv.Cond.Broadcast()
		mutex.Unlock()
	}()
	return nil
}

// Wait returns when the server stops listening. If the server stops
// because Close() was called, it returns nil. If it stops for some
// other reason, it returns the error that caused it to stop.
func (srv *Server) Wait() error {
	srv.Cond.L.Lock()
	defer srv.Cond.L.Unlock()
	for !srv.done {
		log.Print("waiting")
		srv.Cond.Wait()
	}
	return srv.Err
}

// Close shuts down the server. It returns an error if the server
// already stopped for some reason other than a prior call to Close().
func (srv *Server) Close() error {
	srv.shutdown = true
	srv.listener.Close()
	srv.sourceMap.Close()
	return srv.Wait()
}

// Copied from net/http because not exported.
//
type tcpKeepAliveListener struct {
	*net.TCPListener
}

func (ln tcpKeepAliveListener) Accept() (c net.Conn, err error) {
	tc, err := ln.AcceptTCP()
	if err != nil {
		return
	}
	tc.SetKeepAlive(true)
	tc.SetKeepAlivePeriod(3 * time.Minute)
	return tc, nil
}

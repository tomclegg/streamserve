package main

import (
	"io"
	"log"
	"net"
	"net/http"
	"runtime"
	"syscall"
	"time"
)

type Server struct {
	http.Server
	config   Config
	listener *net.TCPListener
	shutdown bool
}

// FlushyResponseWriter wraps http.ResponseWriter, calling Flush()
// after each Write(). Write() panics if the wrapped ResponseWriter
// does not implement the http.Flusher interface.
type FlushyResponseWriter struct {
	http.ResponseWriter
}

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

func (srv *Server) Run(c *Config, listening chan<- string) (err error) {
	// c.Check() is just for tests -- main() has already done this.
	if err = c.Check(); err != nil {
		log.Fatal(err)
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(c.CPUMax))
	defer func() {
		if listening != nil {
			listening <- ""
		}
	}()
	addr, err := net.ResolveTCPAddr("tcp", c.Addr)
	if err != nil {
		return
	}
	srv.listener, err = net.ListenTCP("tcp", addr)
	if err != nil {
		return
	}
	if listening != nil {
		listening <- srv.listener.Addr().String()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(writer http.ResponseWriter, req *http.Request) {
		src := GetSource(c.Path, c)
		fwriter := &FlushyResponseWriter{writer}
		err := NewSink(req.RemoteAddr, fwriter, src, c).Run()
		if e, ok := err.(*net.OpError); ok {
			if e, ok := e.Err.(syscall.Errno); ok {
				if e == syscall.ECONNRESET {
					// Not really an error: client disconnected.
					err = nil
				}
			}
		} else if err == io.EOF {
			// Not really an error.
			err = nil
		}
		if err != nil {
			log.Printf("client %s error: %s", req.RemoteAddr, err)
		}
		if src.gone && src.path == c.Path && !c.Reopen {
			// The only source path has ended and can't be reopened.
			srv.Close()
		}
	})
	srv.Handler = mux
	err = srv.Serve(tcpKeepAliveListener{srv.listener})
	if srv.shutdown {
		err = nil
	}
	return
}

func (srv *Server) Close() {
	srv.shutdown = true
	srv.listener.Close()
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

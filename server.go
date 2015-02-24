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

func (srv *Server) runCtrlChannel(ctrl <-chan string) {
	for cmd := range ctrl {
		log.Print(cmd)
		switch cmd {
		case "shutdown":
			log.Print("ctrl: shutting down")
			srv.shutdown = true
			srv.listener.Close()
		default:
			log.Fatalf("ctrl: unknown command '%s'", cmd)
		}
	}
}

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

func RunNewServer(c *Config, listening chan<- string, ctrl <-chan string) (err error) {
	// c.Check() is just for tests -- main() has already done this.
	if err = c.Check(); err != nil {
		log.Fatal(err)
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(c.CpuMax))
	defer func() {
		if listening != nil {
			listening <- ""
		}
	}()
	srv := &Server{}
	addr, err := net.ResolveTCPAddr("tcp", c.Addr)
	if err != nil {
		return
	}
	srv.listener, err = net.ListenTCP("tcp", addr)
	if err != nil {
		return
	}
	if ctrl != nil {
		go srv.runCtrlChannel(ctrl)
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
			srv.shutdown = true
			srv.listener.Close()
		}
	})
	srv.Handler = mux
	err = srv.Serve(tcpKeepAliveListener{srv.listener})
	if srv.shutdown {
		err = nil
	}
	return
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

package main

import (
	"log"
	"net"
	"net/http"
	"time"
)

type Server struct {
	http.Server
	config Config
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

func RunNewServer(c Config, listening chan<- string) error {
	defer func() { listening <- "" }()
	srv := &Server{}
	addr, err := net.ResolveTCPAddr("tcp", c.Addr)
	if err != nil {
		return err
	}
	ln, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return err
	}
	if listening != nil {
		listening <- ln.Addr().String()
	}
	http.Handle("/", http.HandlerFunc(func (writer http.ResponseWriter, req *http.Request) {
		fwriter := &FlushyResponseWriter{writer}
		label := "client#TODO"
		err := NewSink(label, fwriter, c.Path, c).Run()
		log.Printf("Handler: %s %s", label, err)
	}))
	return srv.Serve(tcpKeepAliveListener{ln})
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

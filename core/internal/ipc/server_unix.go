//go:build !windows

package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
)

type Handlers struct {
	GetStatus func() any
	GetConfig func() any
	Reload    func() error
	Stop      func()
}

type Server struct {
	path     string
	token    string
	handlers Handlers
	ln       net.Listener
	once     sync.Once
}

func DefaultPath() string {
	return filepath.Join(os.TempDir(), "vrc-loghook.sock")
}

func NewServer(path, token string, handlers Handlers) *Server {
	if path == "" {
		path = DefaultPath()
	}
	return &Server{path: path, token: token, handlers: handlers}
}

func (s *Server) Start(ctx context.Context) error {
	_ = os.Remove(s.path)
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return err
	}
	_ = os.Chmod(s.path, 0o600)
	s.ln = ln
	go func() {
		<-ctx.Done()
		s.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			continue
		}
		go s.handleConn(c)
	}
}

func (s *Server) Close() {
	s.once.Do(func() {
		if s.ln != nil {
			_ = s.ln.Close()
		}
		_ = os.Remove(s.path)
	})
}

func (s *Server) handleConn(c net.Conn) {
	defer c.Close()
	dec := json.NewDecoder(bufio.NewReader(c))
	enc := json.NewEncoder(c)
	var req Request
	if err := dec.Decode(&req); err != nil {
		_ = enc.Encode(Response{OK: false, Error: err.Error()})
		return
	}
	if req.Token != s.token {
		_ = enc.Encode(Response{OK: false, Error: "unauthorized"})
		return
	}
	_ = enc.Encode(s.dispatch(req))
}

func (s *Server) dispatch(req Request) Response {
	switch req.Method {
	case "status":
		return Response{OK: true, Body: s.handlers.GetStatus()}
	case "config.get":
		return Response{OK: true, Body: s.handlers.GetConfig()}
	case "config.reload":
		if err := s.handlers.Reload(); err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true}
	case "stop":
		s.handlers.Stop()
		return Response{OK: true}
	default:
		return Response{OK: false, Error: "unknown method"}
	}
}

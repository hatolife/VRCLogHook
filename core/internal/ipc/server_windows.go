//go:build windows

package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"syscall"
	"unsafe"
)

type Handlers struct {
	GetStatus func() any
	GetConfig func() any
	Reload    func() error
	Stop      func()
	GUILog    func(level, message string)
}

type Server struct {
	path     string
	token    string
	handlers Handlers
	once     sync.Once
	closing  chan struct{}
}

const (
	pipeAccessDuplex      = 0x00000003
	pipeTypeByte          = 0x00000000
	pipeReadModeByte      = 0x00000000
	pipeWait              = 0x00000000
	pipeUnlimitedInstance = 255
	errorPipeConnected    = syscall.Errno(535)
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procCreateNamedPipe  = kernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipe = kernel32.NewProc("ConnectNamedPipe")
	procDisconnectPipe   = kernel32.NewProc("DisconnectNamedPipe")
	procCloseHandle      = kernel32.NewProc("CloseHandle")
)

func DefaultPath() string { return `\\.\pipe\vrc-loghook` }

func NewServer(path, token string, handlers Handlers) *Server {
	if path == "" {
		path = DefaultPath()
	}
	return &Server{path: path, token: token, handlers: handlers, closing: make(chan struct{})}
}

func (s *Server) Start(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		s.Close()
	}()
	for {
		select {
		case <-s.closing:
			return nil
		default:
		}
		h, err := createNamedPipe(s.path)
		if err != nil {
			return err
		}
		ok, err := connectNamedPipe(h)
		if !ok && err != nil && !errors.Is(err, errorPipeConnected) {
			closeHandle(h)
			continue
		}
		s.handleConn(h)
		disconnectPipe(h)
		closeHandle(h)
	}
}

func (s *Server) Close() {
	s.once.Do(func() {
		close(s.closing)
	})
}

func (s *Server) handleConn(handle syscall.Handle) {
	f := os.NewFile(uintptr(handle), s.path)
	if f == nil {
		return
	}
	defer f.Close()

	var req Request
	if err := json.NewDecoder(f).Decode(&req); err != nil {
		_ = json.NewEncoder(f).Encode(Response{OK: false, Error: err.Error()})
		return
	}
	if req.Token != s.token {
		_ = json.NewEncoder(f).Encode(Response{OK: false, Error: "unauthorized"})
		return
	}
	_ = json.NewEncoder(f).Encode(s.dispatch(req))
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
	case "gui.log":
		level := "info"
		message := ""
		if m, ok := req.Body.(map[string]any); ok {
			if v, ok := m["level"].(string); ok && v != "" {
				level = v
			}
			if v, ok := m["message"].(string); ok {
				message = v
			}
		}
		if s.handlers.GUILog != nil && message != "" {
			s.handlers.GUILog(level, message)
		}
		return Response{OK: true}
	default:
		return Response{OK: false, Error: "unknown method"}
	}
}

func createNamedPipe(path string) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	r1, _, e1 := procCreateNamedPipe.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(pipeAccessDuplex),
		uintptr(pipeTypeByte|pipeReadModeByte|pipeWait),
		uintptr(pipeUnlimitedInstance),
		uintptr(64*1024),
		uintptr(64*1024),
		0,
		0,
	)
	if r1 == uintptr(syscall.InvalidHandle) {
		if e1 != syscall.Errno(0) {
			return 0, e1
		}
		return 0, syscall.EINVAL
	}
	return syscall.Handle(r1), nil
}

func connectNamedPipe(handle syscall.Handle) (bool, error) {
	r1, _, e1 := procConnectNamedPipe.Call(uintptr(handle), 0)
	if r1 == 0 {
		if e1 != syscall.Errno(0) {
			return false, e1
		}
		return false, syscall.EINVAL
	}
	return true, nil
}

func disconnectPipe(handle syscall.Handle) {
	_, _, _ = procDisconnectPipe.Call(uintptr(handle))
}

func closeHandle(handle syscall.Handle) {
	_, _, _ = procCloseHandle.Call(uintptr(handle))
}

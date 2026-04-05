//go:build !windows

package ipc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerClientMethods(t *testing.T) {
	socket := filepath.Join("/tmp", fmt.Sprintf("vrc-loghook-ipc-test-%d.sock", time.Now().UnixNano()))
	_ = os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("unix socket unavailable in this environment: %v", err)
	}
	_ = ln.Close()
	_ = os.Remove(socket)

	stopped := false
	srv := NewServer(socket, "tok", Handlers{
		GetStatus: func() any { return map[string]any{"running": true} },
		GetConfig: func() any { return map[string]any{"version": "1"} },
		Reload:    func() error { return nil },
		Stop:      func() { stopped = true },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	time.Sleep(80 * time.Millisecond)

	resp, err := Call(socket, Request{Token: "tok", Method: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("status should be ok: %+v", resp)
	}
	resp, err = Call(socket, Request{Token: "bad", Method: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("unauthorized request should fail")
	}
	resp, err = Call(socket, Request{Token: "tok", Method: "unknown.method"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("unknown method should fail")
	}
	resp, err = Call(socket, Request{Token: "tok", Method: "stop"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || !stopped {
		t.Fatal("stop should call handler")
	}
	cancel()
	srv.Close()
}

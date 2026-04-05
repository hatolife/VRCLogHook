//go:build windows

package ipc

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestServerClientMethodsWindows(t *testing.T) {
	pipe := fmt.Sprintf(`\\.\pipe\vrc-loghook-test-%d`, time.Now().UnixNano())
	stopped := false
	srv := NewServer(pipe, "tok", Handlers{
		GetStatus: func() any { return map[string]any{"running": true} },
		GetConfig: func() any { return map[string]any{"version": "1"} },
		Reload:    func() error { return nil },
		Stop:      func() { stopped = true },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	time.Sleep(120 * time.Millisecond)

	resp, err := callEventually(pipe, Request{Token: "tok", Method: "status"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("status should be ok: %+v", resp)
	}
	resp, err = callEventually(pipe, Request{Token: "bad", Method: "status"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("unauthorized request should fail")
	}
	resp, err = callEventually(pipe, Request{Token: "tok", Method: "stop"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || !stopped {
		t.Fatal("stop should call handler")
	}
	cancel()
	srv.Close()
}

func callEventually(pipe string, req Request, timeout time.Duration) (Response, error) {
	deadline := time.Now().Add(timeout)
	var (
		resp Response
		err  error
	)
	for time.Now().Before(deadline) {
		resp, err = Call(pipe, req)
		if err == nil {
			return resp, nil
		}
		time.Sleep(120 * time.Millisecond)
	}
	return Response{}, err
}

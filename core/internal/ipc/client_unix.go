//go:build !windows

package ipc

import (
	"bufio"
	"encoding/json"
	"net"
)

func Call(path string, req Request) (Response, error) {
	c, err := net.Dial("unix", path)
	if err != nil {
		return Response{}, err
	}
	defer c.Close()

	if err := json.NewEncoder(c).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(bufio.NewReader(c)).Decode(&resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

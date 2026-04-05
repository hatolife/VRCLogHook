//go:build !windows

package main

import "net"

func dialIPC(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}

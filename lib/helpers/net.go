package helpers

import (
	"log/slog"
	"net"
	"time"
)

func PortOpen(host string, port string) bool {
	timeout := time.Second
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		slog.Debug("Failed to connect to port", "host", host, "port", port, "error", err)
		return false
	}
	if conn != nil {
		defer conn.Close()
		return true
	}
	return false
}

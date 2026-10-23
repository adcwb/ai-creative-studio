package main

import (
	"errors"
	"net"
	"net/http"
	"time"
)

// Metrics are operator-only, never route this listener through the frontend.
func metricsServer(address string, handler http.Handler) (*http.Server, net.Listener, error) {
	if address == "" {
		return nil, nil, nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, nil, errors.New("METRICS_ADDR must be a loopback IP:port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, nil, errors.New("METRICS_ADDR must bind a literal loopback IP")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", handler)
	return &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}, listener, nil
}

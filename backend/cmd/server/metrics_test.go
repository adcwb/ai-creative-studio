package main

import (
	"net/http"
	"testing"
)

func TestOperatorMetricsLoopbackOnly(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	for _, a := range []string{"0.0.0.0:9090", ":9090", "localhost:9090", "192.0.2.1:9090", "[::]:9090", "invalid"} {
		if _, l, e := metricsServer(a, h); e == nil {
			if l != nil {
				l.Close()
			}
			t.Fatalf("accepted %s", a)
		}
	}
	if s, l, e := metricsServer("", h); s != nil || l != nil || e != nil {
		t.Fatal("disabled metrics")
	}
	s, l, e := metricsServer("127.0.0.1:0", h)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	defer l.Close()
	go s.Serve(l)
	res, e := http.Get("http://" + l.Addr().String() + "/metrics")
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
}

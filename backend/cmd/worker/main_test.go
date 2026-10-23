package main

import "testing"

func TestWorkerConcurrencyBounds(t *testing.T) {
	for _, v := range []string{"-1", "0", "17", "1.5", "x", "9999999999999999999999"} {
		if _, e := workerConcurrency(v); e == nil {
			t.Fatalf("accepted %q", v)
		}
	}
	for v, want := range map[string]int{"": 2, "1": 1, "16": 16} {
		got, e := workerConcurrency(v)
		if e != nil || got != want {
			t.Fatalf("%q %d %v", v, got, e)
		}
	}
}

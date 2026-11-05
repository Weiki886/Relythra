package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeStatusAndRedirect(t *testing.T) {
	for _, status := range []int{200, 204, 302, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Location", "/ok"); w.WriteHeader(status) }))
			defer server.Close()
			err := probe(server.URL)
			if (err == nil) != (status < 300) {
				t.Fatalf("status %d, err %v", status, err)
			}
		})
	}
}

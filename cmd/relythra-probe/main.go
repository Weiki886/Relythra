// relythra-probe is a small HTTP healthcheck for images without a shell or curl.
package main

import (
	"errors"
	"net/http"
	"os"
	"time"
)

func probe(endpoint string) error {
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(endpoint)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("unhealthy HTTP status")
	}
	return nil
}
func main() {
	if len(os.Args) != 2 || probe(os.Args[1]) != nil {
		os.Exit(1)
	}
}

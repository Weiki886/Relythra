package worker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Weiki886/Relythra/internal/worker"
)

func TestHeartbeatStartUpdatesLastBeat(t *testing.T) {
	heartbeat := worker.NewHeartbeat(5 * time.Millisecond)
	before := heartbeat.LastBeat()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		heartbeat.Start(ctx)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for !heartbeat.LastBeat().After(before) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done

	if !heartbeat.LastBeat().After(before) {
		t.Fatal("LastBeat() did not advance after Start()")
	}
}

func TestMetricsHandlerExposesHeartbeatTimestamp(t *testing.T) {
	t.Parallel()

	heartbeat := worker.NewHeartbeat(time.Second)
	response := httptest.NewRecorder()
	heartbeat.MetricsHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if !strings.Contains(response.Body.String(), "relythra_worker_heartbeat_timestamp_seconds") {
		t.Errorf("body = %q, want heartbeat metric", response.Body.String())
	}
}

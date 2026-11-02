package worker

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type Heartbeat struct {
	interval time.Duration
	lastBeat atomic.Int64
}

func NewHeartbeat(interval time.Duration) *Heartbeat {
	heartbeat := &Heartbeat{interval: interval}
	heartbeat.lastBeat.Store(time.Now().UnixNano())
	return heartbeat
}

func (h *Heartbeat) Start(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.lastBeat.Store(now.UnixNano())
		}
	}
}

func (h *Heartbeat) LastBeat() time.Time {
	return time.Unix(0, h.lastBeat.Load())
}

func (h *Heartbeat) MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = fmt.Fprintf(w, "# HELP relythra_worker_heartbeat_timestamp_seconds Unix timestamp of the latest worker heartbeat.\n# TYPE relythra_worker_heartbeat_timestamp_seconds gauge\nrelythra_worker_heartbeat_timestamp_seconds %d\n", h.LastBeat().Unix())
	})
}

// Package telemetry exposes process counters without user IDs or other high-cardinality labels.
package telemetry

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

var mu sync.Mutex
var counters = map[string]uint64{"mono_link_failures_total": 0, "mono_invalid_redirect_total": 0, "avatar_failures_total": 0, "avatar_uploads_total": 0, "mono_webhook_auth_failures_total": 0, "mono_webhook_processing_failures_total": 0, "mono_webhooks_received_total": 0}

func Inc(name string) { mu.Lock(); counters[name]++; mu.Unlock() }
func Write(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	names := make([]string, 0, len(counters))
	for n := range counters {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "# TYPE trackiq_%s counter\ntrackiq_%s %d\n", n, n, counters[n])
	}
}

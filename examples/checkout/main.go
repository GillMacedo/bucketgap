// Command checkout is a small HTTP service used as the example for
// bucketgap. It records request latency in a Prometheus histogram with
// custom buckets and serves the metrics on /metrics.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	latency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "checkout_request_duration_seconds",
		Help:    "Time to serve a checkout request.",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"route", "code"})
	queueWait = prometheus.NewSummary(prometheus.SummaryOpts{
		Name:       "checkout_queue_wait_seconds",
		Help:       "Time a request waits for a worker.",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	})
)

func main() {
	addr := flag.String("listen", "127.0.0.1:8080", "address to listen on")
	flag.Parse()
	prometheus.MustRegister(latency, queueWait)

	http.HandleFunc("/checkout", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		queueWait.Observe(0.002)
		w.Write([]byte("ok\n"))
		latency.WithLabelValues("/checkout", "200").Observe(time.Since(start).Seconds())
	})
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(*addr, nil))
}

package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/canonical/metrics-k8s-proxy/internal/k8s"
	"github.com/canonical/metrics-k8s-proxy/internal/util"
)

// HTTPClient defines the interface for the HTTP client.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// RealHTTPClient is the production implementation of HTTPClient.
type RealHTTPClient struct {
	*http.Client
}

// MetricsHandler holds the HTTP client.
type MetricsHandler struct {
	client HTTPClient
}

// NewMetricsHandler creates a new MetricsHandler with the given HTTP client.
func NewMetricsHandler(client HTTPClient) *MetricsHandler {
	return &MetricsHandler{client: client}
}

// ScrapePodMetrics scrapes metrics from a given pod and returns the combined metrics with the "up" metric.
// In case of errors, it logs them and returns the 'up=0' metric.
func (h *MetricsHandler) ScrapePodMetrics(ctx context.Context, podIP string,
	metricsEndpoint k8s.PodScrapeDetails) string {
	hostPort := net.JoinHostPort(podIP, metricsEndpoint.Port)
	url := fmt.Sprintf("http://%s%s", hostPort, metricsEndpoint.Path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		// Log the error and return the 'up=0' metric
		log.Printf("Error creating request for %s: %v", url, err)
		return util.AppendUpMetric("", metricsEndpoint.PodName, metricsEndpoint.Namespace, 0)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		// Log the error and return the 'up=0' metric
		log.Printf("Error scraping %s: %v", url, err)
		return util.AppendUpMetric("", metricsEndpoint.PodName, metricsEndpoint.Namespace, 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Log the error and return the 'up=0' metric for non-200 responses
		log.Printf("Failed to scrape %s, status code: %d", url, resp.StatusCode)
		return util.AppendUpMetric("", metricsEndpoint.PodName, metricsEndpoint.Namespace, 0)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// Log the error and return the 'up=0' metric for body read errors
		log.Printf("Error reading response from %s: %v", url, err)
		return util.AppendUpMetric("", metricsEndpoint.PodName, metricsEndpoint.Namespace, 0)
	}

	// Append 'up=1' for successful scrape
	labeledMetrics := util.AppendLabels(string(body), metricsEndpoint.PodName, metricsEndpoint.Namespace)
	return util.AppendUpMetric(labeledMetrics, metricsEndpoint.PodName, metricsEndpoint.Namespace, 1)
}

// AggregateMetrics collects metrics from all pods concurrently and returns aggregated results.
func (h *MetricsHandler) AggregateMetrics(ctx context.Context, pw *k8s.PodScrapeWatcher) []string {
	var wg sync.WaitGroup
	var respMu sync.Mutex
	responses := []string{}

	for podIP, metrics := range pw.PodMetricsEndpoints {
		wg.Add(1)

		go func(podIP string, metrics k8s.PodScrapeDetails) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			default:
				metricsResult := h.ScrapePodMetrics(ctx, podIP, metrics)
				respMu.Lock()
				defer respMu.Unlock()
				responses = append(responses, metricsResult)
			}
		}(podIP, metrics)
	}

	// Wait for all goroutines to complete.
	wg.Wait()

	return responses
}

// ProxyMetrics aggregates metrics from all pods, appends pod metadata and 'up' metric, and returns them as text.
func (h *MetricsHandler) ProxyMetrics(w http.ResponseWriter, r *http.Request, pw *k8s.PodScrapeWatcher) {
	ctx := r.Context()
	responses := h.AggregateMetrics(ctx, pw)

	w.Header().Set("Content-Type", "text/plain")

	// If there are responses, write them to the response body.
	if len(responses) > 0 {
		writeResponse(w, strings.Join(responses, "\n"), http.StatusOK)
	} else {
		// No successful metrics or scrapes
		w.WriteHeader(http.StatusNoContent)
	}
}

// writeResponse writes the response body and sets the appropriate status code.
func writeResponse(w http.ResponseWriter, body string, statusCode int) {
	w.WriteHeader(statusCode)
	if _, err := w.Write([]byte(body)); err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
	}
}

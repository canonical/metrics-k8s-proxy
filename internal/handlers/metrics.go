package handlers

import (
	"context"
	"fmt"
	"io"
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

// Do performs an HTTP request.
func (c *RealHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return c.Client.Do(req)
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
func (h *MetricsHandler) ScrapePodMetrics(ctx context.Context, podIP string, metrics k8s.PodMetrics) (string, error) {
	hostPort := net.JoinHostPort(podIP, metrics.Port)
	url := fmt.Sprintf("http://%s%s", hostPort, metrics.Path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return util.AppendUpMetric("", metrics.PodName, metrics.Namespace, 0),
			fmt.Errorf("error creating request for %s: %w", url, err)
	}
	// Perform the HTTP request with context
	resp, err := h.client.Do(req)
	if err != nil {
		// Return up=0 for failed request
		return util.AppendUpMetric("", metrics.PodName, metrics.Namespace, 0),
			fmt.Errorf("error scraping %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Return up=0 for non-200 status code
		return util.AppendUpMetric("", metrics.PodName, metrics.Namespace, 0),
			fmt.Errorf("failed to scrape %s, status code: %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// Return up=0 for read errors
		return util.AppendUpMetric("", metrics.PodName, metrics.Namespace, 0),
			fmt.Errorf("error reading response from %s: %w", url, err)
	}

	// Append 'up=1' for successful scrape
	labeledMetrics := util.AppendLabels(string(body), metrics.PodName, metrics.Namespace)

	return util.AppendUpMetric(labeledMetrics, metrics.PodName, metrics.Namespace, 1), nil
}

// AggregateMetrics collects metrics from all pods concurrently and returns aggregated results.
func (h *MetricsHandler) AggregateMetrics(ctx context.Context, mm *k8s.MetricsManager) ([]string, []string) {
	var wg sync.WaitGroup
	var respMu sync.Mutex
	responses := []string{}
	errors := []string{}
	for podIP, metrics := range mm.GetPodMetricsEndpoints() {
		wg.Add(1)

		go func(podIP string, metrics k8s.PodMetrics) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			default:
				metricsResult, err := h.ScrapePodMetrics(ctx, podIP, metrics)
				respMu.Lock()
				defer respMu.Unlock()
				if err != nil {
					errors = append(errors, err.Error())
				}
				responses = append(responses, metricsResult)
			}
		}(podIP, metrics)
	}

	// Wait for all goroutines to complete.
	wg.Wait()

	return responses, errors
}

// ProxyMetrics aggregates metrics from all pods, appends pod metadata and 'up' metric, and returns them as text.
func (h *MetricsHandler) ProxyMetrics(w http.ResponseWriter, r *http.Request, mm *k8s.MetricsManager) {
	ctx := r.Context()
	responses, errors := h.AggregateMetrics(ctx, mm)

	w.Header().Set("Content-Type", "text/plain")

	// If there are responses, write them to the response body.
	if len(responses) > 0 {
		// Join responses with a newline and write them.
		writeResponse(w, strings.Join(responses, "\n"), http.StatusOK)
		return
	}

	// If there are no responses, but there are errors, return the errors.
	if len(errors) > 0 {
		// Set the appropriate error status code.
		writeResponse(w, strings.Join(errors, "\n"), http.StatusInternalServerError)
		return
	}

	// If there are no responses and no errors, return an appropriate status.
	w.WriteHeader(http.StatusNoContent)
}

// writeResponse writes the response body and sets the appropriate status code.
func writeResponse(w http.ResponseWriter, body string, statusCode int) {
	w.WriteHeader(statusCode)
	if _, err := w.Write([]byte(body)); err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
	}
}

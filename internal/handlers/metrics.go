package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/canonical/metrics-k8s-proxy/internal/util"

	"github.com/canonical/metrics-k8s-proxy/internal/k8s"
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

// Create an HTTP client as a module-level variable.
var Client HTTPClient = &RealHTTPClient{
	Client: &http.Client{},
}

// scrapePodMetrics scrapes metrics from a given pod and returns the combined metrics with the "up" metric.
func ScrapePodMetrics(ctx context.Context, podIP string, metrics k8s.PodMetrics, client HTTPClient) (string, error) {
	url := fmt.Sprintf("http://%s:%s%s", podIP, metrics.Port, metrics.Path)

	// Create an HTTP request with the provided context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		// Return up=0 for request creation error
		return util.AppendUpMetric("", metrics.PodName, metrics.Namespace, 0),
			fmt.Errorf("error creating request for %s: %w", url, err)
	}

	// Perform the HTTP request with context
	resp, err := client.Do(req)
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

// aggregateMetrics collects metrics from all pods concurrently and returns aggregated results.
func AggregateMetrics(ctx context.Context, client HTTPClient) (responses []string, errors []string) {
	var wg sync.WaitGroup
	var respMu sync.Mutex // Mutex for synchronizing access to responses and errors slices.

	// Iterate over the pod metrics endpoints and make parallel HTTP requests.
	for podIP, metrics := range k8s.PodMetricsEndpoints {
		wg.Add(1)

		// Fanning out the HTTP requests in a goroutines.
		go func(podIP string, metrics k8s.PodMetrics) {
			defer wg.Done()

			select {
			case <-ctx.Done(): // Check if the context deadline is exceeded
				return

			default:
				metricsResult, err := ScrapePodMetrics(ctx, podIP, metrics, client)
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

// ProxyMetrics aggregates metrics from all pods,
// appends pod metadata and 'up' metric, and returns them in a HTTP response.
func ProxyMetrics(w http.ResponseWriter, r *http.Request) {
	// Use the context from the HTTP request.
	ctx := r.Context()
	// Aggregate metrics from all pods.
	responses, errors := AggregateMetrics(ctx, Client)

	// Respond with the aggregated metrics.
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

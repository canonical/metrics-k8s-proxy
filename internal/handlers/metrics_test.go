package handlers_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/canonical/metrics-k8s-proxy/internal/handlers"
	"github.com/canonical/metrics-k8s-proxy/internal/k8s"
)

// Mock HTTP Client.
type mockHTTPClient struct {
	responses      map[string]*http.Response
	err            map[string]error
	capturedErrors []string
	delay          time.Duration
}

// Do returns a mocked response or error based on the input URL, with an optional delay.
func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	select {
	case <-req.Context().Done():
		// Return an error if the context is cancelled
		return nil, req.Context().Err()
	default:
		url := req.URL.String()
		if err, exists := m.err[url]; exists {
			m.capturedErrors = append(m.capturedErrors, fmt.Sprintf("failed to scrape %s, %v", url, err))

			return nil, err
		}

		if resp, exists := m.responses[url]; exists {
			return resp, nil
		}

		return nil, fmt.Errorf("no mock response for %s", url)
	}
}

// GetCapturedErrors returns the collected errors during the test.
func (m *mockHTTPClient) getCapturedErrors() []string {
	return m.capturedErrors
}

// mockReadCloser is a mock implementation of io.ReadCloser for simulating read errors.
type mockReadCloser struct {
	err error
}

func (m *mockReadCloser) Read(_ []byte) (int, error) {
	return 0, m.err // Always return the error
}

func (m *mockReadCloser) Close() error {
	return nil // No operation for close
}

func Test_scrapePodMetrics(t *testing.T) {
	type args struct {
		podIP   string
		metrics k8s.PodMetrics
		client  *mockHTTPClient
		ctx     context.Context
	}

	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "Successful Metrics Scrape",
			args: args{
				podIP: "127.0.0.1",
				metrics: k8s.PodMetrics{
					Port:      "8080",
					Path:      "/metrics",
					PodName:   "test-pod",
					Namespace: "test-namespace",
				},
				client: &mockHTTPClient{
					responses: map[string]*http.Response{
						"http://127.0.0.1:8080/metrics": {
							StatusCode: http.StatusOK,
							Body:       io.NopCloser(strings.NewReader("metric1 1\nmetric2 2")),
						},
					},
				},
				ctx: context.Background(),
			},
			want: "metric1{k8s_pod_name=\"test-pod\",k8s_namespace=\"test-namespace\"} 1\n" +
				"metric2{k8s_pod_name=\"test-pod\",k8s_namespace=\"test-namespace\"} 2\n" +
				"up{k8s_pod_name=\"test-pod\",k8s_namespace=\"test-namespace\"} 1\n",
			wantErr: false,
		},
		{
			name: "Network Error",
			args: args{
				podIP: "127.0.0.1",
				metrics: k8s.PodMetrics{
					Port:      "8080",
					Path:      "/metrics",
					PodName:   "test-pod",
					Namespace: "test-namespace",
				},
				client: &mockHTTPClient{
					err: map[string]error{
						"foo": errors.New("network error"),
					},
				},
				ctx: context.Background(),
			},
			want:    "\nup{k8s_pod_name=\"test-pod\",k8s_namespace=\"test-namespace\"} 0\n",
			wantErr: true,
		},
		{
			name: "Non-200 HTTP Status",
			args: args{
				podIP: "127.0.0.1",
				metrics: k8s.PodMetrics{
					Port:      "8080",
					Path:      "/metrics",
					PodName:   "test-pod",
					Namespace: "test-namespace",
				},
				client: &mockHTTPClient{
					responses: map[string]*http.Response{
						"http://127.0.0.1:8080/metrics": {
							StatusCode: http.StatusInternalServerError,
							Body:       io.NopCloser(strings.NewReader("Internal Server Error")),
						},
					},
				},
				ctx: context.Background(),
			},
			want:    "\nup{k8s_pod_name=\"test-pod\",k8s_namespace=\"test-namespace\"} 0\n",
			wantErr: true,
		},
		{
			name: "Read Error",
			args: args{
				podIP: "127.0.0.1",
				metrics: k8s.PodMetrics{
					Port:      "8080",
					Path:      "/metrics",
					PodName:   "test-pod",
					Namespace: "test-namespace",
				},
				client: &mockHTTPClient{
					responses: map[string]*http.Response{
						"http://127.0.0.1:8080/metrics": {
							StatusCode: http.StatusOK,
							Body:       io.NopCloser(&mockReadCloser{err: errors.New("read error")}),
						},
					},
				},
				ctx: context.Background(),
			},
			want:    "\nup{k8s_pod_name=\"test-pod\",k8s_namespace=\"test-namespace\"} 0\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := handlers.NewMetricsHandler(tt.args.client)
			got, err := h.ScrapePodMetrics(tt.args.ctx, tt.args.podIP, tt.args.metrics)
			if (err != nil) != tt.wantErr {
				t.Errorf("scrapePodMetrics() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("scrapePodMetrics() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test_aggregateMetrics tests the aggregateMetrics function.
func Test_aggregateMetrics(t *testing.T) {
	mm := k8s.NewMetricsManager()
	mm.PodMetricsEndpoints = map[string]k8s.PodMetrics{
		"127.0.0.1": {
			Port:      "8080",
			Path:      "/metrics",
			PodName:   "test-pod-1",
			Namespace: "test-namespace",
		},
		"127.0.0.2": {
			Port:      "8080",
			Path:      "/metrics",
			PodName:   "test-pod-2",
			Namespace: "test-namespace",
		},
	}

	type args struct {
		client *mockHTTPClient
		ctx    context.Context
	}
	tests := []struct {
		name    string
		args    args
		want    []string
		wantErr []string
	}{
		{
			name: "Successful Scrapes",
			args: args{
				client: &mockHTTPClient{
					responses: map[string]*http.Response{
						"http://127.0.0.1:8080/metrics": {
							StatusCode: http.StatusOK,
							Body:       io.NopCloser(strings.NewReader("metric1 1\nmetric2 2")),
						},
						"http://127.0.0.2:8080/metrics": {
							StatusCode: http.StatusOK,
							Body:       io.NopCloser(strings.NewReader("metric1 1\nmetric2 2")),
						},
					},
					err: nil,
				},
				ctx: context.Background(),
			},
			want: []string{
				"metric1{k8s_pod_name=\"test-pod-1\",k8s_namespace=\"test-namespace\"} 1\n" +
					"metric2{k8s_pod_name=\"test-pod-1\",k8s_namespace=\"test-namespace\"} 2\n" +
					"up{k8s_pod_name=\"test-pod-1\",k8s_namespace=\"test-namespace\"} 1\n",
				"metric1{k8s_pod_name=\"test-pod-2\",k8s_namespace=\"test-namespace\"} 1\n" +
					"metric2{k8s_pod_name=\"test-pod-2\",k8s_namespace=\"test-namespace\"} 2\n" +
					"up{k8s_pod_name=\"test-pod-2\",k8s_namespace=\"test-namespace\"} 1\n",
			},
			wantErr: []string{},
		},
		{
			name: "Context Deadline Exceeded",
			args: args{
				client: &mockHTTPClient{
					responses: map[string]*http.Response{
						"http://127.0.0.1:8080/metrics": {
							StatusCode: http.StatusOK,
							Body:       io.NopCloser(strings.NewReader("metric1 1\nmetric2 2")),
						},
						"http://127.0.0.2:8080/metrics": {
							StatusCode: http.StatusOK,
							Body:       io.NopCloser(strings.NewReader("metric1 1\nmetric2 2")),
						},
					},
					delay: 2 * time.Second, // Simulate delay to exceed context deadline
				},
				ctx: context.Background(),
			},
			want: []string{
				"\nup{k8s_pod_name=\"test-pod-1\",k8s_namespace=\"test-namespace\"} 0\n",
				"\nup{k8s_pod_name=\"test-pod-2\",k8s_namespace=\"test-namespace\"} 0\n",
			},
			wantErr: []string{
				"error scraping http://127.0.0.1:8080/metrics: context deadline exceeded",
				"error scraping http://127.0.0.2:8080/metrics: context deadline exceeded",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := handlers.NewMetricsHandler(tt.args.client)
			// total context timeout is 1 seconds
			if tt.name == "Context Deadline Exceeded" {
				var cancel context.CancelFunc
				tt.args.ctx, cancel = context.WithTimeout(context.Background(), //nolint:fatcontext // limited to test usage
					1*time.Second)
				defer cancel()
			}

			got, gotErr := h.AggregateMetrics(tt.args.ctx, mm) // Pass context here
			sort.Strings(got)
			sort.Strings(tt.want)
			sort.Strings(gotErr)
			sort.Strings(tt.wantErr)

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("aggregateMetrics() got = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(gotErr, tt.wantErr) {
				t.Errorf("aggregateMetrics() gotErr = %v, wantErr %v", gotErr, tt.wantErr)
			}
		})
	}
}

// Test_ProxyMetrics tests the ProxyMetrics HTTP handler.
func Test_ProxyMetrics(t *testing.T) {
	mm := k8s.NewMetricsManager()
	mm.PodMetricsEndpoints = map[string]k8s.PodMetrics{
		"127.0.0.1": {
			Port:      "8080",
			Path:      "/metrics",
			PodName:   "test-pod-1",
			Namespace: "test-namespace",
		},
		"127.0.0.2": {
			Port:      "8080",
			Path:      "/metrics",
			PodName:   "test-pod-2",
			Namespace: "test-namespace",
		},
	}

	tests := []struct {
		name             string
		mockClient       *mockHTTPClient
		expectedResponse string
		expectedErrors   []string
	}{
		{
			name: "Successful Proxy Metrics",
			mockClient: &mockHTTPClient{
				responses: map[string]*http.Response{
					"http://127.0.0.1:8080/metrics": {
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader("metric1 1\nmetric2 2")),
					},
					"http://127.0.0.2:8080/metrics": {
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader("metric1 1\nmetric2 2")),
					},
				},
			},
			expectedResponse: "metric1{k8s_pod_name=\"test-pod-1\",k8s_namespace=\"test-namespace\"} 1\n" +
				"metric2{k8s_pod_name=\"test-pod-1\",k8s_namespace=\"test-namespace\"} 2\n" +
				"up{k8s_pod_name=\"test-pod-1\",k8s_namespace=\"test-namespace\"} 1\n" +
				"\nmetric1{k8s_pod_name=\"test-pod-2\",k8s_namespace=\"test-namespace\"} 1\n" +
				"metric2{k8s_pod_name=\"test-pod-2\",k8s_namespace=\"test-namespace\"} 2\n" +
				"up{k8s_pod_name=\"test-pod-2\",k8s_namespace=\"test-namespace\"} 1\n",
			expectedErrors: nil,
		},
		{
			name: "Partial Success with Errors",
			mockClient: &mockHTTPClient{
				responses: map[string]*http.Response{
					"http://127.0.0.1:8080/metrics": {
						StatusCode: http.StatusInternalServerError,
						Body:       io.NopCloser(strings.NewReader("Internal Server Error")),
					},
					"http://127.0.0.2:8080/metrics": {
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader("metric1 1\nmetric2 2")),
					},
				},
			},
			expectedResponse: "\nup{k8s_pod_name=\"test-pod-1\",k8s_namespace=\"test-namespace\"} 0\n" +
				"\nmetric1{k8s_pod_name=\"test-pod-2\",k8s_namespace=\"test-namespace\"} 1\n" +
				"metric2{k8s_pod_name=\"test-pod-2\",k8s_namespace=\"test-namespace\"} 2\n" +
				"up{k8s_pod_name=\"test-pod-2\",k8s_namespace=\"test-namespace\"} 1\n",
			expectedErrors: nil,
		},
		{
			name: "All Failures",
			mockClient: &mockHTTPClient{
				responses: map[string]*http.Response{
					"http://127.0.0.1:8080/metrics": {
						StatusCode: http.StatusInternalServerError,
						Body:       io.NopCloser(strings.NewReader("Internal Server Error")),
					},
					"http://127.0.0.2:8080/metrics": {
						StatusCode: http.StatusInternalServerError,
						Body:       io.NopCloser(strings.NewReader("Internal Server Error")),
					},
				},
			},
			expectedResponse: "\nup{k8s_pod_name=\"test-pod-1\",k8s_namespace=\"test-namespace\"} 0\n" + "\n" +
				"\nup{k8s_pod_name=\"test-pod-2\",k8s_namespace=\"test-namespace\"} 0\n",
			expectedErrors: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a ResponseRecorder to capture the response
			rr := httptest.NewRecorder()
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}

			// Override the global client with the mock client for the duration of this test
			h := handlers.NewMetricsHandler(tt.mockClient)
			// Call the ProxyMetrics function
			h.ProxyMetrics(rr, req, mm)

			// Check the response
			gotResponse := rr.Body.String()
			expectedResponse := tt.expectedResponse

			// Sort and compare the response strings
			if !compareSortedStrings(gotResponse, expectedResponse) {
				t.Errorf("ProxyMetrics() got = %v, want %v", gotResponse, expectedResponse)
			}
			// Sort and compare errors if any
			var gotErrors []string
			if len(tt.expectedErrors) > 0 {
				gotErrors = tt.mockClient.getCapturedErrors()
			}

			// Sort both slices for comparison
			sort.Strings(gotErrors)
			sort.Strings(tt.expectedErrors)

			if !reflect.DeepEqual(gotErrors, tt.expectedErrors) {
				t.Errorf("ProxyMetrics() errors = %v, want %v", gotErrors, tt.expectedErrors)
			}
		})
	}
}

// compareSortedStrings splits, sorts, and joins two strings for comparison.
func compareSortedStrings(a, b string) bool {
	linesA := strings.Split(a, "\n")
	linesB := strings.Split(b, "\n")

	sort.Strings(linesA)
	sort.Strings(linesB)

	sortedA := strings.Join(linesA, "\n")
	sortedB := strings.Join(linesB, "\n")

	return sortedA == sortedB
}

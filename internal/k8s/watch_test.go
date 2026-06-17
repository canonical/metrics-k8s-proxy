package k8s_test

import (
	"context"
	"log/slog"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/canonical/metrics-k8s-proxy/internal/k8s"
)

func TestUpdatePodMetrics(t *testing.T) {
	t.Parallel()

	type args struct {
		pod *corev1.Pod
	}
	tests := []struct {
		name     string
		args     args
		expected k8s.PodScrapeDetails
		wantIP   string
	}{
		{
			name: "Valid pod with scrape enabled",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Annotations: map[string]string{
							"prometheus.io/scrape": "true",
							"prometheus.io/port":   "8080",
							"prometheus.io/path":   "/custom-metrics",
						},
					},
					Status: corev1.PodStatus{
						PodIP: "10.0.0.1",
					},
				},
			},
			expected: k8s.PodScrapeDetails{
				Port:      "8080",
				Path:      "/custom-metrics",
				PodName:   "test-pod",
				Namespace: "default",
			},
			wantIP: "10.0.0.1",
		},
		{
			name: "Valid pod with no custom path",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "no-custom-pod",
						Namespace: "default",
						Annotations: map[string]string{
							"prometheus.io/scrape": "true",
						},
					},
					Status: corev1.PodStatus{
						PodIP: "10.0.0.2",
					},
				},
			},
			expected: k8s.PodScrapeDetails{
				Port:      "80",
				Path:      "/metrics",
				PodName:   "no-custom-pod",
				Namespace: "default",
			},
			wantIP: "10.0.0.2",
		},
		{
			name: "Pod without IP",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "no-ip-pod",
						Namespace: "default",
						Annotations: map[string]string{
							"prometheus.io/scrape": "true",
						},
					},
					Status: corev1.PodStatus{
						PodIP: "",
					},
				},
			},
			expected: k8s.PodScrapeDetails{},
			wantIP:   "",
		},
		{
			name: "Pod without scrape annotation",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "no-scrape-pod",
						Namespace: "default",
						Annotations: map[string]string{
							"prometheus.io/port": "9090",
						},
					},
					Status: corev1.PodStatus{
						PodIP: "10.0.0.2",
					},
				},
			},
			expected: k8s.PodScrapeDetails{},
			wantIP:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pw := k8s.NewPodScrapeWatcher(slog.Default())

			pw.UpdatePodMetrics(tt.args.pod)

			if tt.wantIP != "" {
				if got, exists := pw.PodMetricsEndpoints[tt.wantIP]; !exists || !reflect.DeepEqual(got, tt.expected) {
					t.Errorf("Expected PodMetricsEndpoints[%v] = %v, but got %v", tt.wantIP, tt.expected, got)
				}
			}
		})
	}
}

func TestDeletePodMetrics(t *testing.T) {
	t.Parallel()

	type args struct {
		pod *corev1.Pod
	}
	tests := []struct {
		name   string
		args   args
		wantIP string
	}{
		{
			name: "Valid pod with existing metrics",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "delete-pod",
						Namespace: "default",
					},
					Status: corev1.PodStatus{
						PodIP: "10.0.0.1",
					},
				},
			},
			wantIP: "10.0.0.1",
		},
		{
			name: "Pod with no IP",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "no-ip-pod",
						Namespace: "default",
					},
					Status: corev1.PodStatus{
						PodIP: "",
					},
				},
			},
			wantIP: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pw := k8s.NewPodScrapeWatcher(slog.Default())
			// Pre-populate PodMetricsEndpoints with a sample pod to test deletion.
			pw.PodMetricsEndpoints = map[string]k8s.PodScrapeDetails{
				"10.0.0.1": {
					PodName:   "delete-pod",
					Namespace: "default",
				},
			}

			pw.DeletePodMetrics(tt.args.pod)

			if tt.wantIP != "" {
				if _, exists := pw.PodMetricsEndpoints[tt.wantIP]; exists {
					t.Errorf("Expected PodMetricsEndpoints[%v] to be deleted, but it still exists", tt.wantIP)
				}
			}
		})
	}
}

// triggerWatchEvent sends the appropriate event to the fake watcher based on event type.
func triggerWatchEvent(
	fakeWatcher *watch.FakeWatcher,
	pod *corev1.Pod,
	eventType watch.EventType,
	handleUpdateCalled *atomic.Bool,
) {
	switch eventType {
	case watch.Added:
		fakeWatcher.Add(pod)
	case watch.Modified:
		fakeWatcher.Modify(pod)
	case watch.Deleted:
		// The informer requires the object to be in its cache before
		// it can process a Delete event, so add it first.
		fakeWatcher.Add(pod)
		time.Sleep(100 * time.Millisecond)
		handleUpdateCalled.Store(false)
		fakeWatcher.Delete(pod)
	case watch.Error, watch.Bookmark:
		// No action needed
	}
}

// assertHandlerCalled verifies the correct handler was invoked for the given event type.
func assertHandlerCalled(
	t *testing.T,
	eventType watch.EventType,
	handleUpdateCalled, handleDeleteCalled *atomic.Bool,
	wantCalled bool,
) {
	t.Helper()

	switch eventType {
	case watch.Added, watch.Modified:
		if handleUpdateCalled.Load() != wantCalled {
			t.Errorf("UpdatePodMetricsFunc was not called when expected")
		}
	case watch.Deleted:
		if handleDeleteCalled.Load() != wantCalled {
			t.Errorf("DeletePodMetricsFunc was not called when expected")
		}
	case watch.Error, watch.Bookmark:
		// No handler expected for these event types
	}
}

// TestWatchPods tests the WatchPods function of the PodScrapeWatcher.
func TestWatchPods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		namespace  string
		labels     map[string]string
		eventType  watch.EventType
		wantCalled bool
	}{
		{
			name:       "UpdatePodMetricsFunc is called when pod added",
			namespace:  "default",
			labels:     map[string]string{"app": "test"},
			eventType:  watch.Added,
			wantCalled: true,
		},
		{
			name:       "UpdatePodMetricsFunc is called when pod modified",
			namespace:  "default",
			labels:     map[string]string{"app": "test"},
			eventType:  watch.Modified,
			wantCalled: true,
		},
		{
			name:       "DeletePodMetricsFunc is called when pod deleted",
			namespace:  "default",
			labels:     map[string]string{"app": "test"},
			eventType:  watch.Deleted,
			wantCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pw := k8s.NewPodScrapeWatcher(slog.Default())
			fakeClientset := fake.NewSimpleClientset()
			fakeWatcher := watch.NewFake()
			fakeClientset.PrependWatchReactor("pods", func(_ clienttesting.Action) (bool, watch.Interface, error) {
				return true, fakeWatcher, nil
			})

			// Prepare for the test
			var handleUpdateCalled atomic.Bool
			var handleDeleteCalled atomic.Bool

			// Mock the UpdatePodMetricsFunc and DeletePodMetricsFunc for the test
			pw.UpdatePodMetricsFunc = func(_ *corev1.Pod) {
				handleUpdateCalled.Store(true)
			}
			pw.DeletePodMetricsFunc = func(_ *corev1.Pod) {
				handleDeleteCalled.Store(true)
			}

			// Run WatchPods in a goroutine since it blocks until context cancellation
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			errCh := make(chan error, 1)
			go func() {
				errCh <- pw.WatchPods(ctx, fakeClientset, tt.namespace, tt.labels)
			}()

			// Give the informer time to start and sync
			time.Sleep(100 * time.Millisecond)

			// Simulate different pod events
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: tt.namespace,
					Labels:    tt.labels,
					Annotations: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   "8080",
						"prometheus.io/path":   "/metrics",
					},
				},
			}

			// Trigger the event based on the test case
			triggerWatchEvent(fakeWatcher, pod, tt.eventType, &handleUpdateCalled)

			// Allow some time for the event to be processed
			time.Sleep(100 * time.Millisecond)

			assertHandlerCalled(t, tt.eventType, &handleUpdateCalled, &handleDeleteCalled, tt.wantCalled)

			// Cancel the context and verify WatchPods exits cleanly.
			cancel()
			select {
			case watchErr := <-errCh:
				if watchErr != nil {
					t.Errorf("WatchPods returned unexpected error: %v", watchErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("WatchPods did not exit after context cancellation")
			}
		})
	}
}

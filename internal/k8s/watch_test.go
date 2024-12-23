package k8s_test

import (
	"bytes"
	"log"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/canonical/metrics-k8s-proxy/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

// captureLogOutput captures log output during the execution of a function.
func captureLogOutput(f func()) string {
	var buf bytes.Buffer

	// Temporarily set the default logger output to the buffer
	log.SetOutput(&buf)
	defer func() {
		// Reset the logger output to the default after capturing the logs
		log.SetOutput(os.Stderr)
	}()

	// Execute the function passed in
	f()

	// Return the captured log output as a string
	return buf.String()
}

func TestUpdatePodMetrics(t *testing.T) {
	pw := k8s.NewPodScrapeWatcher()

	type args struct {
		pod *corev1.Pod
	}
	tests := []struct {
		name     string
		args     args
		expected k8s.PodScrapeDetails
		wantIP   string
		wantLogs string
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
			wantIP:   "10.0.0.1",
			wantLogs: "Updated pod test-pod with IP 10.0.0.1",
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
			wantIP:   "10.0.0.2",
			wantLogs: "Updated pod no-custom-pod with IP 10.0.0.2",
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
			wantLogs: "",
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
			wantLogs: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear the PodMetricsEndpoints map for a clean test.
			pw.PodMetricsEndpoints = make(map[string]k8s.PodScrapeDetails)

			logOutput := captureLogOutput(func() {
				pw.UpdatePodMetrics(tt.args.pod)
			})

			if tt.wantIP != "" {
				if got, exists := pw.PodMetricsEndpoints[tt.wantIP]; !exists || !reflect.DeepEqual(got, tt.expected) {
					t.Errorf("Expected PodMetricsEndpoints[%v] = %v, but got %v", tt.wantIP, tt.expected, got)
				}
			}

			// Check if log message matches
			if tt.wantLogs != "" && !strings.Contains(logOutput, tt.wantLogs) {
				t.Errorf("Expected log to contain %q, but got %q", tt.wantLogs, logOutput)
			}
		})
	}
}

func TestDeletePodMetrics(t *testing.T) {
	pw := k8s.NewPodScrapeWatcher()

	type args struct {
		pod *corev1.Pod
	}
	tests := []struct {
		name     string
		args     args
		wantIP   string
		wantLogs string
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
			wantIP:   "10.0.0.1",
			wantLogs: "Deleted pod delete-pod with IP 10.0.0.1",
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
			wantIP:   "",
			wantLogs: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Pre-populate PodMetricsEndpoints with a sample pod to test deletion.
			pw.PodMetricsEndpoints = map[string]k8s.PodScrapeDetails{
				"10.0.0.1": {
					PodName:   "delete-pod",
					Namespace: "default",
				},
			}

			logOutput := captureLogOutput(func() {
				pw.DeletePodMetrics(tt.args.pod)
			})

			if tt.wantIP != "" {
				if _, exists := pw.PodMetricsEndpoints[tt.wantIP]; exists {
					t.Errorf("Expected PodMetricsEndpoints[%v] to be deleted, but it still exists", tt.wantIP)
				}
			}

			// Check if log message matches
			if tt.wantLogs != "" && !strings.Contains(logOutput, tt.wantLogs) {
				t.Errorf("Expected log to contain %q, but got %q", tt.wantLogs, logOutput)
			}
		})
	}
}

// TestWatchPods tests the WatchPods function of the PodScrapeWatcher.
func TestWatchPods(t *testing.T) {
	type args struct {
		clientset kubernetes.Interface
		namespace string
		labels    map[string]string
	}

	pw := k8s.NewPodScrapeWatcher()

	// Create a fake Kubernetes client
	fakeClientset := fake.NewSimpleClientset()

	// Create a fake pod watch
	fakeWatcher := watch.NewFake()
	fakeClientset.PrependWatchReactor("pods", func(_ clienttesting.Action) (bool, watch.Interface, error) {
		return true, fakeWatcher, nil
	})

	tests := []struct {
		name       string
		args       args
		eventType  watch.EventType
		wantCalled bool
	}{
		{
			name: "UpdatePodMetricsFunc is called when pod added",
			args: args{
				clientset: fakeClientset,
				namespace: "default",
				labels:    map[string]string{"app": "test"},
			},
			eventType:  watch.Added,
			wantCalled: true,
		},
		{
			name: "UpdatePodMetricsFunc is called when pod modified",
			args: args{
				clientset: fakeClientset,
				namespace: "default",
				labels:    map[string]string{"app": "test"},
			},
			eventType:  watch.Modified,
			wantCalled: true,
		},
		{
			name: "DeletePodMetricsFunc is called when pod deleted",
			args: args{
				clientset: fakeClientset,
				namespace: "default",
				labels:    map[string]string{"app": "test"},
			},
			eventType:  watch.Deleted,
			wantCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Prepare for the test
			handleUpdateCalled := false
			handleDeleteCalled := false

			// Mock the UpdatePodMetricsFunc and DeletePodMetricsFunc for the test
			pw.UpdatePodMetricsFunc = func(_ *corev1.Pod) {
				handleUpdateCalled = true
			}
			pw.DeletePodMetricsFunc = func(_ *corev1.Pod) {
				handleDeleteCalled = true
			}

			// Resetting the pod metrics map for isolation
			pw.PodMetricsEndpoints = make(map[string]k8s.PodScrapeDetails)

			// Run WatchPods in a goroutine since it blocks indefinitely
			go pw.WatchPods(tt.args.clientset, tt.args.namespace, tt.args.labels)

			// Simulate different pod events
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: tt.args.namespace,
					Labels:    tt.args.labels,
					Annotations: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   "8080",
						"prometheus.io/path":   "/metrics",
					},
				},
			}

			// Trigger the event based on the test case
			switch tt.eventType {
			case watch.Added:
				fakeWatcher.Add(pod)
			case watch.Modified:
				fakeWatcher.Modify(pod)
			case watch.Deleted:
				fakeWatcher.Delete(pod)
			case watch.Error:
				break
			case watch.Bookmark:
				break
			}

			// Allow some time for the event to be processed
			time.Sleep(100 * time.Millisecond)

			// Check if the appropriate handler was called
			if tt.eventType == watch.Added || tt.eventType == watch.Modified {
				if handleUpdateCalled != tt.wantCalled {
					t.Errorf("UpdatePodMetricsFunc was not called when expected")
				}
			} else if tt.eventType == watch.Deleted {
				if handleDeleteCalled != tt.wantCalled {
					t.Errorf("DeletePodMetricsFunc was not called when expected")
				}
			}
		})
	}
}

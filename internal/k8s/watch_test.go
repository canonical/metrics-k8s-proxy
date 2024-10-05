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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

func Test_updatePodMetrics(t *testing.T) {
	type args struct {
		pod *corev1.Pod
	}
	tests := []struct {
		name     string
		args     args
		expected k8s.PodMetrics
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
			expected: k8s.PodMetrics{
				Port:      "8080",
				Path:      "/custom-metrics",
				PodName:   "test-pod",
				Namespace: "default",
			},
			wantIP:   "10.0.0.1",
			wantLogs: "Updated pod test-pod with IP 10.0.0.1",
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
			expected: k8s.PodMetrics{},
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
			expected: k8s.PodMetrics{},
			wantIP:   "",
			wantLogs: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear the PodMetricsEndpoints map for a clean test.
			k8s.PodMetricsEndpoints = make(map[string]k8s.PodMetrics)

			logOutput := captureLogOutput(func() {
				k8s.UpdatePodMetrics(tt.args.pod)
			})

			if tt.wantIP != "" {
				if got, exists := k8s.PodMetricsEndpoints[tt.wantIP]; !exists || !reflect.DeepEqual(got, tt.expected) {
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

func Test_deletePodMetrics(t *testing.T) {
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
			k8s.PodMetricsEndpoints = map[string]k8s.PodMetrics{
				"10.0.0.1": {
					PodName:   "delete-pod",
					Namespace: "default",
				},
			}

			logOutput := captureLogOutput(func() {
				k8s.DeletePodMetrics(tt.args.pod)
			})

			if tt.wantIP != "" {
				if _, exists := k8s.PodMetricsEndpoints[tt.wantIP]; exists {
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

// Define a mock invalid object that does not fully implement runtime.Object
type InvalidObject struct{}

func (i *InvalidObject) GetObjectKind() schema.ObjectKind { return nil }
func (i *InvalidObject) DeepCopyObject() runtime.Object   { return i }

func Test_handlePodEvent(t *testing.T) {
	// Mocks to check if the methods were called
	updateCalled := false
	deleteCalled := false

	// Mock implementations
	mockUpdatePodMetrics := func(_ *corev1.Pod) {
		updateCalled = true
	}
	mockDeletePodMetrics := func(_ *corev1.Pod) {
		deleteCalled = true
	}

	// Replace the real functions with mocks
	k8s.UpdatePodMetricsFunc = mockUpdatePodMetrics
	k8s.DeletePodMetricsFunc = mockDeletePodMetrics

	// Ensure to reset the function variables after the test
	defer func() {
		k8s.UpdatePodMetricsFunc = k8s.UpdatePodMetrics
		k8s.DeletePodMetricsFunc = k8s.DeletePodMetrics
	}()

	tests := []struct {
		name         string
		event        watch.Event
		expectUpdate bool
		expectDelete bool
	}{
		{
			name: "Pod Added",
			event: watch.Event{
				Type:   watch.Added,
				Object: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}, Status: corev1.PodStatus{PodIP: "192.168.0.1"}},
			},
			expectUpdate: true,
			expectDelete: false,
		},
		{
			name: "Pod Modified",
			event: watch.Event{
				Type:   watch.Modified,
				Object: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}, Status: corev1.PodStatus{PodIP: "192.168.0.2"}},
			},
			expectUpdate: true,
			expectDelete: false,
		},
		{
			name: "Pod Deleted",
			event: watch.Event{
				Type:   watch.Deleted,
				Object: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}, Status: corev1.PodStatus{PodIP: "192.168.0.3"}},
			},
			expectUpdate: false,
			expectDelete: true,
		},
		{
			name: "Invalid Event Object",
			event: watch.Event{
				Type:   watch.Added,
				Object: &InvalidObject{},
			},
			expectUpdate: false,
			expectDelete: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset the tracking variables
			updateCalled = false
			deleteCalled = false

			k8s.HandlePodEvent(tt.event)

			if tt.expectUpdate && !updateCalled {
				t.Errorf("Expected updatePodMetrics to be called, but it wasn't")
			}
			if tt.expectDelete && !deleteCalled {
				t.Errorf("Expected deletePodMetrics to be called, but it wasn't")
			}
		})
	}
}

func TestWatchPods(t *testing.T) {
	type args struct {
		clientset kubernetes.Interface
		namespace string
		labels    map[string]string
	}

	// Mock for handlePodEventFunc
	var handlePodEventCalled bool
	var lastEventType watch.EventType
	k8s.HandlePodEventFunc = func(event watch.Event) {
		handlePodEventCalled = true
		lastEventType = event.Type
	}

	// Cleanup: Restore original handlePodEventFunc after the test
	defer func() {
		k8s.HandlePodEventFunc = k8s.HandlePodEvent
	}()

	// Create a fake Kubernetes client
	fakeClientset := fake.NewSimpleClientset()

	// Create a fake pod watch
	fakeWatcher := watch.NewFake()
	fakeClientset.PrependWatchReactor("pods", func(action clienttesting.Action) (handled bool, ret watch.Interface, err error) {
		return true, fakeWatcher, nil
	})

	tests := []struct {
		name       string
		args       args
		eventType  watch.EventType
		wantCalled bool
	}{
		{
			name: "handlePodEventFunc is called when pod added",
			args: args{
				clientset: fakeClientset,
				namespace: "default",
				labels:    map[string]string{"app": "test"},
			},
			eventType:  watch.Added,
			wantCalled: true,
		},
		{
			name: "handlePodEventFunc is called when pod modified",
			args: args{
				clientset: fakeClientset,
				namespace: "default",
				labels:    map[string]string{"app": "test"},
			},
			eventType:  watch.Modified,
			wantCalled: true,
		},
		{
			name: "handlePodEventFunc is called when pod deleted",
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
			// Reset the flag before each test
			handlePodEventCalled = false
			lastEventType = ""

			// Run WatchPods in a goroutine since it blocks indefinitely
			go k8s.WatchPods(tt.args.clientset, tt.args.namespace, tt.args.labels)

			// Simulate different pod events
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: tt.args.namespace,
				},
			}

			// Trigger the event based on test case
			switch tt.eventType {
			case watch.Added:
				fakeWatcher.Add(pod)
			case watch.Modified:
				fakeWatcher.Modify(pod)
			case watch.Deleted:
				fakeWatcher.Delete(pod)
			}

			// Give some time for the event to be processed
			time.Sleep(100 * time.Millisecond)

			// Check if handlePodEventFunc was called
			if handlePodEventCalled != tt.wantCalled {
				t.Errorf("handlePodEventFunc was not called when expected")
			}

			// Validate that the event type matches
			if lastEventType != tt.eventType {
				t.Errorf("Expected event type %v, but got %v", tt.eventType, lastEventType)
			}
		})
	}
}

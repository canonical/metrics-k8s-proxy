package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// PodScrapeDetails stores the metrics endpoint details and metadata for a pod.
type PodScrapeDetails struct {
	Port      string
	Path      string
	PodName   string
	Namespace string
}

// PodScrapeWatcher manages pod metrics and provides methods to handle updates and deletions.
type PodScrapeWatcher struct {
	PodMetricsEndpoints map[string]PodScrapeDetails
	mu                  sync.Mutex
	logger              *slog.Logger

	// Function variables for update and delete operations, to allow mocking during tests.
	UpdatePodMetricsFunc func(*corev1.Pod)
	DeletePodMetricsFunc func(*corev1.Pod)
}

const defaultResyncPeriod = 10 * time.Minute

// NewPodScrapeWatcher initializes a new PodScrapeWatcher with default function implementations.
func NewPodScrapeWatcher(logger *slog.Logger) *PodScrapeWatcher {
	if logger == nil {
		logger = slog.Default()
	}

	pw := &PodScrapeWatcher{
		PodMetricsEndpoints: make(map[string]PodScrapeDetails),
		logger:              logger,
	}
	pw.UpdatePodMetricsFunc = pw.UpdatePodMetrics
	pw.DeletePodMetricsFunc = pw.DeletePodMetrics

	return pw
}

// GetPodMetricsEndpoints returns a copy of the current pod metrics endpoints.
func (pw *PodScrapeWatcher) GetPodMetricsEndpoints() map[string]PodScrapeDetails {
	pw.mu.Lock()
	defer pw.mu.Unlock()

	endpointsCopy := make(map[string]PodScrapeDetails, len(pw.PodMetricsEndpoints))
	for k, v := range pw.PodMetricsEndpoints {
		endpointsCopy[k] = v
	}
	return endpointsCopy
}

// WatchPods starts the SharedInformer to monitor pod events and updates the metrics endpoints accordingly.
// It blocks until the provided context is cancelled. Returns an error if event handler registration
// or initial cache sync fails.
func (pw *PodScrapeWatcher) WatchPods(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace string,
	labels map[string]string,
) error {
	factory := informers.NewSharedInformerFactoryWithOptions(
		clientset,
		defaultResyncPeriod,
		informers.WithNamespace(namespace),
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: labels})
		}),
	)

	podInformer := factory.Core().V1().Pods().Informer()

	// Add event handlers for pod add/update/delete
	if _, err := podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				pw.logger.Error("Error casting added object to Pod")
				return
			}
			pw.UpdatePodMetricsFunc(pod)
		},
		UpdateFunc: func(_, newObj interface{}) {
			newPod, ok := newObj.(*corev1.Pod)
			if !ok {
				pw.logger.Error("Error casting updated object to Pod")
				return
			}
			pw.UpdatePodMetricsFunc(newPod)
		},
		DeleteFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				// When the informer's watch reconnects after a disconnect, deletes
				// that occurred during the gap arrive as tombstones.
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					pw.logger.Error("Error casting deleted object to Pod")
					return
				}
				pod, ok = tombstone.Obj.(*corev1.Pod)
				if !ok {
					pw.logger.Error("Tombstone contained unexpected object type")
					return
				}
			}
			pw.DeletePodMetricsFunc(pod)
		},
	}); err != nil {
		return fmt.Errorf("failed to add event handler: %w", err)
	}

	// Derive a stopCh from the context so callers control the lifecycle.
	stopCh := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(stopCh)
	}()

	factory.Start(stopCh)
	// Wait for the informer cache to sync
	if !cache.WaitForCacheSync(stopCh, podInformer.HasSynced) {
		return fmt.Errorf("failed to sync pod cache")
	}

	// Block until the context is cancelled
	<-stopCh

	return nil
}

// UpdatePodMetrics updates or adds pod metrics based on the pod annotations.
func (pw *PodScrapeWatcher) UpdatePodMetrics(pod *corev1.Pod) {
	annotations := pod.GetAnnotations()
	if scrape, exists := annotations["prometheus.io/scrape"]; exists && scrape == "true" {
		podIP := pod.Status.PodIP
		if podIP == "" {
			return
		}

		port := annotations["prometheus.io/port"]
		if port == "" {
			port = "80"
		}
		path := annotations["prometheus.io/path"]
		if path == "" {
			path = "/metrics"
		}

		// Store the pod IP, port, path, and additional metadata like name and namespace.
		pw.mu.Lock()
		pw.PodMetricsEndpoints[podIP] = PodScrapeDetails{
			Port:      port,
			Path:      path,
			PodName:   pod.Name,
			Namespace: pod.Namespace,
		}
		pw.mu.Unlock()

		pw.logger.Info("Updated pod", "pod", pod.Name, "ip", podIP)
	}
}

// DeletePodMetrics removes the pod metrics entry when a pod is deleted.
func (pw *PodScrapeWatcher) DeletePodMetrics(pod *corev1.Pod) {
	podIP := pod.Status.PodIP
	if podIP != "" {
		pw.mu.Lock()
		delete(pw.PodMetricsEndpoints, podIP)
		pw.mu.Unlock()

		pw.logger.Info("Deleted pod", "pod", pod.Name, "ip", podIP)
	}
}

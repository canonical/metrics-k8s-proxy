package k8s

import (
	"context"
	"fmt"
	"log"
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

	// Function variables for update and delete operations, to allow mocking during tests.
	UpdatePodMetricsFunc func(*corev1.Pod)
	DeletePodMetricsFunc func(*corev1.Pod)
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

const defaultResyncPeriod = 10 * time.Minute

// NewPodScrapeWatcher initializes a new PodScrapeWatcher with default function implementations.
func NewPodScrapeWatcher() *PodScrapeWatcher {
	pw := &PodScrapeWatcher{
		PodMetricsEndpoints: make(map[string]PodScrapeDetails),
	}
	pw.UpdatePodMetricsFunc = pw.UpdatePodMetrics
	pw.DeletePodMetricsFunc = pw.DeletePodMetrics

	return pw
}

// WatchPods starts the SharedInformer to monitor pod events and updates the metrics endpoints accordingly.
func (pw *PodScrapeWatcher) WatchPods(ctx context.Context, clientset kubernetes.Interface, namespace string, labels map[string]string) {
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
				log.Println("Error casting added object to Pod")
				return
			}
			pw.UpdatePodMetricsFunc(pod)
		},
		UpdateFunc: func(_, newObj interface{}) {
			newPod, ok := newObj.(*corev1.Pod)
			if !ok {
				log.Println("Error casting updated object to Pod")
				return
			}
			pw.UpdatePodMetricsFunc(newPod)
		},
		DeleteFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				log.Println("Error casting deleted object to Pod")
				return
			}
			pw.DeletePodMetricsFunc(pod)
		},
	}); err != nil {
		log.Fatalf("Failed to add event handler: %v", err)
	}

	// Create a stop channel that will be closed when the context is done
	stopCh := make(chan struct{})
	defer close(stopCh)

	// Start the informer
	factory.Start(stopCh)

	// Wait for the informer cache to sync
	if !cache.WaitForCacheSync(stopCh, podInformer.HasSynced) {
		log.Println("Failed to sync pod cache")
		return
	}

	// Wait for context cancellation
	<-ctx.Done()
	log.Println("Pod watcher stopping...")
}

// ValidationError represents an error that occurred during pod validation
type ValidationError struct {
	PodName   string
	Namespace string
	Reason    string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid pod configuration %s/%s: %s", e.Namespace, e.PodName, e.Reason)
}

// podMetricsConfig holds the configuration for pod metrics scraping
type podMetricsConfig struct {
	podIP     string
	port      string
	path      string
	podName   string
	namespace string
}

func (c *podMetricsConfig) validate() error {
	if c.podIP == "" {
		return &ValidationError{
			PodName:   c.podName,
			Namespace: c.namespace,
			Reason:    "pod IP is empty",
		}
	}
	if c.port == "" {
		return &ValidationError{
			PodName:   c.podName,
			Namespace: c.namespace,
			Reason:    "port is empty",
		}
	}
	if c.path == "" {
		return &ValidationError{
			PodName:   c.podName,
			Namespace: c.namespace,
			Reason:    "path is empty",
		}
	}
	return nil
}

// shouldScrapePod checks if the pod should be scraped based on its annotations
func shouldScrapePod(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	annotations := pod.GetAnnotations()
	if annotations == nil {
		return false
	}
	scrape, exists := annotations["prometheus.io/scrape"]
	return exists && scrape == "true"
}

// getMetricsConfig extracts metrics configuration from pod annotations
func getMetricsConfig(pod *corev1.Pod) (*podMetricsConfig, error) {
	if pod.Status.PodIP == "" {
		return nil, &ValidationError{
			PodName:   pod.Name,
			Namespace: pod.Namespace,
			Reason:    "pod IP is not assigned",
		}
	}

	annotations := pod.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	config := &podMetricsConfig{
		podIP:     pod.Status.PodIP,
		port:      annotations["prometheus.io/port"],
		path:      annotations["prometheus.io/path"],
		podName:   pod.Name,
		namespace: pod.Namespace,
	}

	// Set defaults
	if config.port == "" {
		config.port = "80"
	}
	if config.path == "" {
		config.path = "/metrics"
	}

	if err := config.validate(); err != nil {
		return nil, err
	}

	return config, nil
}

// UpdatePodMetrics updates or adds pod metrics based on the pod annotations.
func (pw *PodScrapeWatcher) UpdatePodMetrics(pod *corev1.Pod) {
	if pod == nil {
		log.Println("Received nil pod in UpdatePodMetrics")
		return
	}

	if !shouldScrapePod(pod) {
		return
	}

	config, err := getMetricsConfig(pod)
	if err != nil {
		log.Printf("Failed to get metrics config for pod %s/%s: %v",
			pod.Namespace, pod.Name, err)
		return
	}

	// Store the pod metrics configuration
	pw.mu.Lock()
	pw.PodMetricsEndpoints[config.podIP] = PodScrapeDetails{
		Port:      config.port,
		Path:      config.path,
		PodName:   config.podName,
		Namespace: config.namespace,
	}
	pw.mu.Unlock()

	log.Printf("Updated pod %s with IP %s", config.podName, config.podIP)
}

// DeletePodMetrics removes the pod metrics entry when a pod is deleted.
func (pw *PodScrapeWatcher) DeletePodMetrics(pod *corev1.Pod) {
	if pod == nil {
		log.Println("Received nil pod in DeletePodMetrics")
		return
	}

	podIP := pod.Status.PodIP
	if podIP == "" {
		return
	}

	pw.mu.Lock()
	if _, exists := pw.PodMetricsEndpoints[podIP]; exists {
		delete(pw.PodMetricsEndpoints, podIP)
		log.Printf("Deleted pod %s with IP %s", pod.Name, podIP)
	}
	pw.mu.Unlock()
}

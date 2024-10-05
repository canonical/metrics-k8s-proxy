package k8s

import (
	"context"
	"log"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
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
	HandlePodEventFunc   func(watch.Event)
}

// NewPodScrapeWatcher initializes a new PodScrapeWatcher with default function implementations.
func NewPodScrapeWatcher() *PodScrapeWatcher {
	pw := &PodScrapeWatcher{
		PodMetricsEndpoints: make(map[string]PodScrapeDetails),
	}
	pw.HandlePodEventFunc = pw.HandlePodEvent
	pw.UpdatePodMetricsFunc = pw.UpdatePodMetrics
	pw.DeletePodMetricsFunc = pw.DeletePodMetrics

	return pw
}

// GetPodMetricsEndpoints returns the current pod metrics endpoints.
func (pw *PodScrapeWatcher) GetPodMetricsEndpoints() map[string]PodScrapeDetails {
	pw.mu.Lock()
	defer pw.mu.Unlock()

	// Return a copy of the PodMetricsEndpoints to avoid race conditions
	endpointsCopy := make(map[string]PodScrapeDetails)
	for k, v := range pw.PodMetricsEndpoints {
		endpointsCopy[k] = v
	}
	return endpointsCopy
}

// WatchPods watches for pod changes and updates the metrics endpoints accordingly.
func (pw *PodScrapeWatcher) WatchPods(clientset kubernetes.Interface, namespace string, labels map[string]string) {
	labelSelector := metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: labels})
	for {
		watcher, err := clientset.CoreV1().Pods(namespace).Watch(context.TODO(), metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			log.Fatalf("Error watching pods: %v", err)
		}

		for event := range watcher.ResultChan() {
			pw.HandlePodEventFunc(event)
		}
	}
}

// HandlePodEvent processes the pod events and updates the pod metrics endpoints.
func (pw *PodScrapeWatcher) HandlePodEvent(event watch.Event) {
	pod, ok := event.Object.(*corev1.Pod)
	if !ok {
		log.Println("Error casting event object to Pod")
		return
	}

	pw.mu.Lock()
	defer pw.mu.Unlock()

	switch event.Type {
	case watch.Added, watch.Modified:
		pw.UpdatePodMetricsFunc(pod)
	case watch.Deleted:
		pw.DeletePodMetricsFunc(pod)
	case watch.Bookmark:
		// No action needed for bookmark events.
	case watch.Error:
		log.Printf("Error event occurred: %v", event.Object)
	}
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
		pw.PodMetricsEndpoints[podIP] = PodScrapeDetails{
			Port:      port,
			Path:      path,
			PodName:   pod.Name,
			Namespace: pod.Namespace,
		}
		log.Printf("Updated pod %s with IP %s", pod.Name, podIP)
	}
}

// DeletePodMetrics removes the pod metrics entry when a pod is deleted.
func (pw *PodScrapeWatcher) DeletePodMetrics(pod *corev1.Pod) {
	podIP := pod.Status.PodIP
	if podIP != "" {
		delete(pw.PodMetricsEndpoints, podIP)
		log.Printf("Deleted pod %s with IP %s", pod.Name, podIP)
	}
}

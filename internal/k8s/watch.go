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

// PodMetrics stores the metrics endpoint details and metadata for a pod.
type PodMetrics struct {
	Port      string
	Path      string
	PodName   string
	Namespace string
}

// PodMetricsEndpoints stores all pod metrics endpoints, synchronized by a mutex.
var (
	PodMetricsEndpoints = make(map[string]PodMetrics)
	mu                  sync.Mutex

	// Function variables for update and delete operations, to allow mocking during tests.
	UpdatePodMetricsFunc = UpdatePodMetrics
	DeletePodMetricsFunc = DeletePodMetrics
	HandlePodEventFunc   = HandlePodEvent
)

// WatchPods watches for pod changes and updates the metrics endpoints accordingly.
func WatchPods(clientset kubernetes.Interface, namespace string, labels map[string]string) {
	labelSelector := metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: labels})
	for {
		watcher, err := clientset.CoreV1().Pods(namespace).Watch(context.TODO(), metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			log.Fatalf("Error watching pods: %v", err)
		}

		for event := range watcher.ResultChan() {
			HandlePodEventFunc(event)
		}
	}
}

// handlePodEvent processes the pod events and updates the pod metrics endpoints.
func HandlePodEvent(event watch.Event) {
	pod, ok := event.Object.(*corev1.Pod)
	if !ok {
		log.Println("Error casting event object to Pod")

		return
	}

	mu.Lock()
	defer mu.Unlock()

	switch event.Type {
	case watch.Added, watch.Modified:
		UpdatePodMetricsFunc(pod)
	case watch.Deleted:
		DeletePodMetricsFunc(pod)
	}
}

// UpdatePodMetrics updates or adds pod metrics based on the pod annotations.
func UpdatePodMetrics(pod *corev1.Pod) {
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

		// Store the pod IP, port, path, and additional metadata like name and namespace
		PodMetricsEndpoints[podIP] = PodMetrics{
			Port:      port,
			Path:      path,
			PodName:   pod.Name,
			Namespace: pod.Namespace,
		}
		log.Printf("Updated pod %s with IP %s", pod.Name, podIP)
	}
}

// DeletePodMetrics removes the pod metrics entry when a pod is deleted.
func DeletePodMetrics(pod *corev1.Pod) {
	podIP := pod.Status.PodIP
	if podIP != "" {
		delete(PodMetricsEndpoints, podIP)
		log.Printf("Deleted pod %s with IP %s", pod.Name, podIP)
	}
}

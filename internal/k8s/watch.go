package k8s

import (
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
func (pw *PodScrapeWatcher) WatchPods(clientset kubernetes.Interface, namespace string, labels map[string]string) {
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

	// Start the informer
	stopCh := make(chan struct{})
	factory.Start(stopCh)
	// Wait for the informer cache to sync
	if !cache.WaitForCacheSync(stopCh, podInformer.HasSynced) {
		close(stopCh) // Explicitly close the channel before exiting
		log.Fatal("Failed to sync pod cache")
	}

	// Block until stopCh is closed
	<-stopCh
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

		log.Printf("Updated pod %s with IP %s", pod.Name, podIP)
	}
}

// DeletePodMetrics removes the pod metrics entry when a pod is deleted.
func (pw *PodScrapeWatcher) DeletePodMetrics(pod *corev1.Pod) {
	podIP := pod.Status.PodIP
	if podIP != "" {
		pw.mu.Lock()
		delete(pw.PodMetricsEndpoints, podIP)
		pw.mu.Unlock()

		log.Printf("Deleted pod %s with IP %s", pod.Name, podIP)
	}
}

package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/canonical/metrics-k8s-proxy/internal/handlers"
	"github.com/canonical/metrics-k8s-proxy/internal/k8s"
	"github.com/canonical/metrics-k8s-proxy/internal/util"
	"k8s.io/client-go/kubernetes"

	"github.com/gorilla/mux"
)

// Parses the label selector and timeout from command line arguments.
func parseFlags() (map[string]string, time.Duration) {
	labelSelector := flag.String("labels", "", "Label selector for watching pods (e.g., 'app=ztunnel')")
	timeout := flag.Duration("timeout", 9*time.Second, "HTTP server read and write timeout (e.g., 15s, 1m)")
	flag.Parse()

	if *labelSelector == "" {
		log.Fatal("Label selector is required (e.g., --labels app=ztunnel)")
	}

	return util.ParseLabels(*labelSelector), *timeout
}

// Initializes the Kubernetes client.
func initK8sClient() kubernetes.Interface {
	_, clientset, err := k8s.GetKubernetesClient(k8s.DefaultBuildConfigFunc, k8s.DefaultNewClientsetFunc)
	if err != nil {
		log.Fatalf("Error building Kubernetes config: %v", err)
	}

	return clientset
}

// Starts the HTTP server.
func startServer(timeout time.Duration, mm *k8s.MetricsManager) *http.Server {
	r := mux.NewRouter()

	httpClient := &handlers.RealHTTPClient{Client: &http.Client{}}
	metricsHandler := handlers.NewMetricsHandler(httpClient)

	r.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metricsHandler.ProxyMetrics(w, r, mm) // mm is the instance of MetricsManager
	}).Methods(http.MethodGet)

	server := &http.Server{
		Handler:      r,
		Addr:         "0.0.0.0:15090",
		WriteTimeout: timeout,
		ReadTimeout:  timeout,
	}

	return server
}

func main() {
	// Parse label selector and timeout for Kubernetes pods
	labels, timeout := parseFlags()

	// Initialize Kubernetes client and start watching pods
	clientset := initK8sClient()
	// Create an instance of MetricsManager
	metricsManager := k8s.NewMetricsManager()

	go metricsManager.WatchPods(clientset, "", labels)
	// Start the HTTP server
	server := startServer(timeout, metricsManager)

	log.Println("Starting metrics proxy server on port 15090")
	log.Fatal(server.ListenAndServe())
}

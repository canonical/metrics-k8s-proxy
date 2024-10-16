package main

import (
	"context"
	"flag"
	"fmt"
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
func parseFlags() (map[string]string, time.Duration, string) {
	labelSelector := flag.String("labels", "", "Label selector for watching pods (e.g., 'app=ztunnel')")
	scrapeTimeout := flag.Duration("scrape_timeout",
		9*time.Second, "Maximum allowed time for any given scrape (e.g., 15s, 1m)")
	port := flag.String("port", "15090", "Port on which pods' metrics will be exposed")

	flag.Parse()

	if *labelSelector == "" {
		log.Fatal("Label selector is required (e.g., --labels app=ztunnel)")
	}

	return util.ParseLabels(*labelSelector), *scrapeTimeout, *port
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
func startServer(scrapeTimeout time.Duration, port string, pw *k8s.PodScrapeWatcher) *http.Server {
	r := mux.NewRouter()

	httpClient := &handlers.RealHTTPClient{Client: &http.Client{}}
	metricsHandler := handlers.NewMetricsHandler(httpClient)

	r.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		// Create a new context with a timeout based on the scrapeTimeout
		ctx, cancel := context.WithTimeout(r.Context(), scrapeTimeout)
		defer cancel()

		metricsHandler.ProxyMetrics(w, r.WithContext(ctx), pw)
	}).Methods(http.MethodGet)

	server := &http.Server{
		Handler: r,
		Addr:    fmt.Sprintf("0.0.0.0:%s", port),

		// Below isn't tied to the context passed to the http server, but rather a global write timeout
		// if we hit the below timeout we get an empty reply from server
		// TODO: Do we need this?, seems redunant since it's not tied to context timeout
		WriteTimeout: scrapeTimeout * 2, //nolint:mnd // Set to double the scrape interval to avoid timing out
		// Below is added as a guard to Potential DoS Slowloris Attack
		ReadHeaderTimeout: scrapeTimeout * 2, //nolint:mnd // Set to double the scrape interval to avoid timing out
	}

	return server
}

func main() {
	// Parse label selector and scrapeTimeout for Kubernetes pods
	labels, scrapeTimeout, port := parseFlags()

	// Initialize Kubernetes client and start watching pods
	clientset := initK8sClient()
	// Create an instance of PodScrapeWatcher
	podWatcher := k8s.NewPodScrapeWatcher()

	go podWatcher.WatchPods(clientset, "", labels)
	// Start the HTTP server
	server := startServer(scrapeTimeout, port, podWatcher)

	log.Println("Starting metrics proxy server on port 15090")
	log.Fatal(server.ListenAndServe())
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/canonical/metrics-k8s-proxy/internal/handlers"
	"github.com/canonical/metrics-k8s-proxy/internal/k8s"
	"github.com/canonical/metrics-k8s-proxy/internal/util"
	"k8s.io/client-go/kubernetes"

	"github.com/gorilla/mux"
)

const defaultScrapeTimeout = 9 * time.Second

// Parses the label selector, timeout, and port from environment variables.
func ParseEnvVars() (map[string]string, time.Duration, string, error) {
	labelSelector := os.Getenv("POD_LABEL_SELECTOR")
	scrapeTimeoutEnv := os.Getenv("SCRAPE_TIMEOUT")
	port := os.Getenv("PORT")

	// Parse the labels
	if labelSelector == "" {
		return nil, 0, "", errors.New("environment variable POD_LABEL_SELECTOR is required")
	}
	labels := util.ParseLabels(labelSelector)
	if len(labels) == 0 {
		return nil, 0, "", errors.New("invalid or empty label selector provided, please ensure valid labels are set")
	}
	if port == "" {
		port = "15090" // Default port value
	}

	// Default scrape timeout value
	scrapeTimeout := defaultScrapeTimeout
	if scrapeTimeoutEnv != "" {
		parsedTimeout, err := time.ParseDuration(scrapeTimeoutEnv)
		if err != nil {
			return nil, 0, "", fmt.Errorf("invalid value for SCRAPE_TIMEOUT: %w", err)
		}
		scrapeTimeout = parsedTimeout
	}

	return labels, scrapeTimeout, port, nil
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

func showHelp() {
	log.Println(`Usage: metrics-proxy [--help]`)

	log.Println(`
Environment Variables:
  POD_LABEL_SELECTOR: Label selector for watching pods (e.g., "app=ztunnel"). Required.
  SCRAPE_TIMEOUT: Maximum allowed time for any given scrape (e.g., "15s", "1m"). Default is "9s".
  PORT: Port on which the metrics proxy will expose aggregated metrics collected from watched pods.
        Default is "15090".`)
	os.Exit(0)
}

func main() {
	// Parse help flag
	help := flag.Bool("help", false, "Show usage information")
	flag.Parse()

	if *help {
		showHelp()
	}
	// Parse label selector and scrapeTimeout for Kubernetes pods
	labels, scrapeTimeout, port, err := ParseEnvVars()
	if err != nil {
		log.Printf("Error: %v\n", err)
		showHelp()
	}

	// Initialize Kubernetes client and start watching pods
	clientset := initK8sClient()
	// Create an instance of PodScrapeWatcher
	podWatcher := k8s.NewPodScrapeWatcher()

	go podWatcher.WatchPods(clientset, "", labels)
	// Start the HTTP server
	server := startServer(scrapeTimeout, port, podWatcher)

	log.Printf("Starting metrics proxy on port %s", port)
	log.Printf("Scrape timeout set to: %v", scrapeTimeout)
	log.Printf("Watching pods with labels: %v", labels)
	log.Fatal(server.ListenAndServe())
}

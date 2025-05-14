package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/canonical/metrics-k8s-proxy/internal/config"
	"github.com/canonical/metrics-k8s-proxy/internal/handlers"
	"github.com/canonical/metrics-k8s-proxy/internal/k8s"
	"k8s.io/client-go/kubernetes"

	"github.com/gorilla/mux"
)

// Initializes the Kubernetes client.
func initK8sClient() kubernetes.Interface {
	_, clientset, err := k8s.GetKubernetesClient(k8s.DefaultBuildConfigFunc, k8s.DefaultNewClientsetFunc)
	if err != nil {
		log.Fatalf("Error building Kubernetes config: %v", err)
	}

	return clientset
}

// Starts the HTTP server.
func startServer(timeout time.Duration, address string, port string, pw *k8s.PodScrapeWatcher) *http.Server {
	r := mux.NewRouter()

	httpClient := &handlers.RealHTTPClient{Client: &http.Client{}}
	metricsHandler := handlers.NewMetricsHandler(httpClient)

	r.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		// Create a new context with a timeout based on the timeout
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		metricsHandler.ProxyMetrics(w, r.WithContext(ctx), pw)
	}).Methods(http.MethodGet)

	server := &http.Server{
		Handler: r,
		Addr:    fmt.Sprintf("%s:%s", address, port),

		// Below isn't tied to the context passed to the http server, but rather a global write timeout
		// if we hit the below timeout we get an empty reply from server
		// TODO: Do we need this?, seems redunant since it's not tied to context timeout
		WriteTimeout: timeout * 2, //nolint:mnd // Set to double the scrape interval to avoid timing out
		// Below is added as a guard to Potential DoS Slowloris Attack
		ReadHeaderTimeout: timeout * 2, //nolint:mnd // Set to double the scrape interval to avoid timing out
	}

	return server
}

func showHelp() {
	fmt.Println(`Usage: metrics-proxy [--help]`)

	fmt.Println(`
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

	var err error
	cfg := &config.Config{}

	labels, err := cfg.Labels()
	if err != nil {
		log.Printf("Error: %v\n", err)
		showHelp()
	}

	scrapeTimeout, err := cfg.ScrapeTimeout()
	if err != nil {
		log.Printf("Error: %v\n", err)
		showHelp()
	}

	port := cfg.Port()
	address := cfg.Address()

	// Initialize Kubernetes client and start watching pods
	clientset := initK8sClient()
	// Create an instance of PodScrapeWatcher
	podWatcher := k8s.NewPodScrapeWatcher()

	go podWatcher.WatchPods(clientset, "", labels)
	// Start the HTTP server
	server := startServer(scrapeTimeout, address, port, podWatcher)

	log.Printf("Starting metrics proxy on port %s", port)
	log.Printf("Scrape timeout set to: %v", scrapeTimeout)
	log.Printf("Watching pods with labels: %v", labels)
	log.Fatal(server.ListenAndServe())
}

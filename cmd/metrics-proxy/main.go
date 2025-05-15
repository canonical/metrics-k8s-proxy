package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
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

	port, err := cfg.Port()
	if err != nil {
		log.Fatalf("Error: %v", err)
	}

	address := cfg.Address()

	// Initialize Kubernetes client and start watching pods
	clientset := initK8sClient()

	// Create a context that we can cancel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create an instance of PodScrapeWatcher
	podWatcher := k8s.NewPodScrapeWatcher()

	// Start watching pods with cancellable context
	go podWatcher.WatchPods(ctx, clientset, "", labels)

	// Start the HTTP server
	server := startServer(scrapeTimeout, address, port, podWatcher)

	// Set up graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Starting metrics proxy on port %s", port)
		log.Printf("Scrape timeout set to: %v", scrapeTimeout)
		log.Printf("Watching pods with labels: %v", labels)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error starting server: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-stop
	log.Println("Shutting down gracefully...")

	// Create shutdown context with 30 second timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Shutdown the server
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error during server shutdown: %v", err)
	}

	// Cancel the pod watcher
	cancel()

	log.Println("Server shutdown complete")
}

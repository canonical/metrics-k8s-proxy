package main

import (
	"os"
	"testing"
	"time"
)

// resetEnvVars registers cleanup logic to remove them after the test completes.
func resetEnvVars(t *testing.T) {
	t.Cleanup(func() {
		// Unset environment variables to avoid side effects between tests
		os.Unsetenv("POD_LABEL_SELECTOR")
		os.Unsetenv("SCRAPE_TIMEOUT")
		os.Unsetenv("PORT")
	})
}

func TestPodSelector_Valid(t *testing.T) {
	// Set environment variables
	resetEnvVars(t)
	t.Setenv("POD_LABEL_SELECTOR", "app=ztunnel")
	labels, _, _, err := ParseEnvVars()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(labels) == 0 || labels["app"] != "ztunnel" {
		t.Errorf("Expected label selector 'app=ztunnel', got %v", labels)
	}
}

func TestParseEnvVars_Valid(t *testing.T) {
	resetEnvVars(t)

	// Set environment variables
	t.Setenv("POD_LABEL_SELECTOR", "app=ztunnel")
	t.Setenv("SCRAPE_TIMEOUT", "10s")
	t.Setenv("PORT", "8080")

	labels, scrapeTimeout, port, err := ParseEnvVars()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(labels) == 0 || labels["app"] != "ztunnel" {
		t.Errorf("Expected label selector 'app=ztunnel', got %v", labels)
	}
	if scrapeTimeout != 10*time.Second {
		t.Errorf("Expected scrapeTimeout '10s', got %v", scrapeTimeout)
	}
	if port != "8080" {
		t.Errorf("Expected port '8080', got %v", port)
	}
}

func TestParseEnvVars_InvalidScrapeTimeout(t *testing.T) {
	resetEnvVars(t)
	// Set environment variables with an invalid SCRAPE_TIMEOUT
	t.Setenv("POD_LABEL_SELECTOR", "app=ztunnel")
	t.Setenv("SCRAPE_TIMEOUT", "invalid")
	t.Setenv("PORT", "8080")

	_, _, _, err := ParseEnvVars()
	if err == nil || err.Error() != "invalid value for SCRAPE_TIMEOUT: time: invalid duration \"invalid\"" {
		t.Errorf("Expected error due to invalid SCRAPE_TIMEOUT, but got %v", err)
	}
}

func TestParseEnvVars_MissingPodLabelSelector(t *testing.T) {
	resetEnvVars(t)
	// Set environment variables without POD_LABEL_SELECTOR
	t.Setenv("SCRAPE_TIMEOUT", "10s")
	t.Setenv("PORT", "8080")

	_, _, _, err := ParseEnvVars()
	if err == nil || err.Error() != "environment variable POD_LABEL_SELECTOR is required" {
		t.Errorf("Expected error due to missing POD_LABEL_SELECTOR, but got %v", err)
	}
}

func TestPodSelector_Invalid(t *testing.T) {
	resetEnvVars(t)
	// Set invalid POD_LABEL_SELECTOR
	t.Setenv("POD_LABEL_SELECTOR", "invalid@#45")
	_, _, _, err := ParseEnvVars()
	if err == nil || err.Error() != "invalid or empty label selector provided, please ensure valid labels are set" {
		t.Errorf("Expected error due to invalid POD_LABEL_SELECTOR, but got %v", err)
	}
}

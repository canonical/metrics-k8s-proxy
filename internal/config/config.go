package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/canonical/metrics-k8s-proxy/internal/util"
)

const DEFAULT_PORT = "15090"
const DEFAULT_SCRAPE_TIMEOUT = 9 * time.Second
const DEFAULT_ADDRESS = "0.0.0.0"

type Config struct{}

func (cfg *Config) Address() string {
	return DEFAULT_ADDRESS
}

func (cfg *Config) Port() string {
	env := os.Getenv("PORT")
	if env == "" {
		return DEFAULT_PORT
	}
	return env
}

func (cfg *Config) ScrapeTimeout() (time.Duration, error) {
	env := os.Getenv("SCRAPE_TIMEOUT")

	if env == "" {
		return DEFAULT_SCRAPE_TIMEOUT, nil
	}

	if timeout, err := time.ParseDuration(env); err != nil {
		return 0, fmt.Errorf("invalid value for SCRAPE_TIMEOUT: %w", err)
	} else {
		return timeout, nil
	}
}

func (cfg *Config) Labels() (map[string]string, error) {
	env := os.Getenv("POD_LABEL_SELECTOR")

	if env == "" {
		return nil, errors.New("environment variable POD_LABEL_SELECTOR is required, but was not set")
	}

	labels := util.ParseLabels(env)
	if len(labels) == 0 {
		return nil, errors.New("invalid or empty label selector provided, please ensure valid labels are set")
	}

	return labels, nil
}

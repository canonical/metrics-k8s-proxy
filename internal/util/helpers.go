package util

import (
	"fmt"
	"strings"
)

const (
	KeyValuePartsLength = 2
	MetricPartsLength   = 2
)

// ParseLabels parses a label selector string into a map.
func ParseLabels(labelString string) map[string]string {
	labels := map[string]string{}
	for _, pair := range strings.Split(labelString, ",") {
		if keyValue := strings.Split(pair, "="); len(keyValue) == KeyValuePartsLength {
			labels[keyValue[0]] = keyValue[1]
		}
	}

	return labels
}

// appendLabels adds pod-specific labels to each metric.
// this was added to allow metrics distinction if multiple pods are reporting the same metric.
func AppendLabels(metricsData, podName, namespace string) string {
	// Split the metrics into lines
	lines := strings.Split(metricsData, "\n")

	// Prepend pod and namespace labels to each metric line, where applicable
	var labeledMetrics []string
	for _, line := range lines {
		// Skip comments and empty lines
		if strings.HasPrefix(line, "#") || line == "" {
			labeledMetrics = append(labeledMetrics, line)

			continue
		}

		// If the line already contains labels, insert pod-specific labels inside the existing braces.
		// Otherwise, add the labels between the metric name and its value.
		if strings.Contains(line, "{") {
			// Insert the pod and namespace labels within the existing labels.
			line = strings.Replace(line, "{", fmt.Sprintf("{k8s_pod_name=\"%s\",k8s_namespace=\"%s\",", podName, namespace), 1)
		} else {
			// Add the labels before the value (after the metric name).
			parts := strings.Fields(line)
			if len(parts) == MetricPartsLength {
				metricName := parts[0]
				metricValue := parts[1]
				line = fmt.Sprintf("%s{k8s_pod_name=\"%s\",k8s_namespace=\"%s\"} %s", metricName, podName, namespace, metricValue)
			}
		}

		labeledMetrics = append(labeledMetrics, line)
	}

	return strings.Join(labeledMetrics, "\n")
}

// AppendUpMetric appends the 'up' metric to the existing metrics data based on the pod's scrape status.
func AppendUpMetric(metricsData, podName, namespace string, status int) string {
	// Generate the 'up' metric based on the status
	upMetric := fmt.Sprintf("up{k8s_pod_name=\"%s\",k8s_namespace=\"%s\"} %d\n", podName, namespace, status)

	// Append the 'up' metric to the metrics data
	return fmt.Sprintf("%s\n%s", metricsData, upMetric)
}

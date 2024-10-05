package util_test

import (
	"reflect"
	"testing"

	"github.com/canonical/metrics-k8s-proxy/internal/util"
)

func TestParseLabels(t *testing.T) {
	type args struct {
		labelString string
	}
	tests := []struct {
		name string
		args args
		want map[string]string
	}{
		{
			name: "single label",
			args: args{labelString: "app=metrics"},
			want: map[string]string{"app": "metrics"},
		},
		{
			name: "multiple labels",
			args: args{labelString: "app=metrics,env=prod"},
			want: map[string]string{"app": "metrics", "env": "prod"},
		},
		{
			name: "empty string",
			args: args{labelString: ""},
			want: map[string]string{},
		},
		{
			name: "invalid label format",
			args: args{labelString: "app=metrics,env"},
			want: map[string]string{"app": "metrics"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := util.ParseLabels(tt.args.labelString); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseLabels() = %v, want %v", got, tt.want)
				t.Logf("Got: %q", got)
				t.Logf("Want: %q", tt.want)
			}
		})
	}
}

func TestAppendLabels(t *testing.T) {
	type args struct {
		metricsData string
		podName     string
		namespace   string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "simple metric without labels",
			args: args{
				metricsData: "http_requests_total 5",
				podName:     "pod1",
				namespace:   "default",
			},
			want: "http_requests_total{k8s_pod_name=\"pod1\",k8s_namespace=\"default\"} 5",
		},
		{
			name: "metric with existing labels",
			args: args{
				metricsData: "http_requests_total{method=\"GET\"} 5",
				podName:     "pod1",
				namespace:   "default",
			},
			want: "http_requests_total{k8s_pod_name=\"pod1\",k8s_namespace=\"default\",method=\"GET\"} 5",
		},
		{
			name: "multiple metrics",
			args: args{
				metricsData: "http_requests_total 5\ncpu_usage 90",
				podName:     "pod1",
				namespace:   "default",
			},
			want: "http_requests_total{k8s_pod_name=\"pod1\",k8s_namespace=\"default\"} 5\ncpu_usage{k8s_pod_name=\"pod1\",k8s_namespace=\"default\"} 90",
		},
		{
			name: "empty metric data",
			args: args{
				metricsData: "",
				podName:     "pod1",
				namespace:   "default",
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := util.AppendLabels(tt.args.metricsData, tt.args.podName, tt.args.namespace)
			if got != tt.want {
				t.Errorf("AppendLabels() got = %v, want %v", got, tt.want)
				t.Logf("Got: %q", got)
				t.Logf("Want: %q", tt.want)
			}
		})
	}
}

func TestAppendUpMetric(t *testing.T) {
	type args struct {
		metricsData string
		podName     string
		namespace   string
		status      int
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "append up metric to existing data",
			args: args{
				metricsData: "http_requests_total 5",
				podName:     "pod1",
				namespace:   "default",
				status:      1,
			},
			want: "http_requests_total 5\nup{k8s_pod_name=\"pod1\",k8s_namespace=\"default\"} 1\n",
		},
		{
			name: "append up metric to empty data",
			args: args{
				metricsData: "",
				podName:     "pod1",
				namespace:   "default",
				status:      0,
			},
			want: "\nup{k8s_pod_name=\"pod1\",k8s_namespace=\"default\"} 0\n",
		},
		{
			name: "append with status 0",
			args: args{
				metricsData: "cpu_usage 90",
				podName:     "pod1",
				namespace:   "default",
				status:      0,
			},
			want: "cpu_usage 90\nup{k8s_pod_name=\"pod1\",k8s_namespace=\"default\"} 0\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := util.AppendUpMetric(tt.args.metricsData, tt.args.podName, tt.args.namespace, tt.args.status); got != tt.want {
				t.Errorf("AppendUpMetric() = %v, want %v", got, tt.want)
				t.Logf("Got: %q", got)
				t.Logf("Want: %q", tt.want)
			}
		})
	}
}

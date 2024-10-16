package k8s_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/canonical/metrics-k8s-proxy/internal/k8s"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

// Mock functions for testing.
func mockBuildConfigSuccess() (*rest.Config, error) {
	return &rest.Config{Host: "https://mock-cluster"}, nil
}

func mockBuildConfigFailure() (*rest.Config, error) {
	return nil, errors.New("failed to build config")
}

func mockNewClientsetSuccess(_ *rest.Config) (kubernetes.Interface, error) {
	return fake.NewSimpleClientset(), nil
}

func mockNewClientsetFailure(_ *rest.Config) (kubernetes.Interface, error) {
	return nil, errors.New("failed to create clientset")
}

func TestGetKubernetesClient(t *testing.T) {
	tests := []struct {
		name       string
		buildFunc  func() (*rest.Config, error)
		clientFunc func(*rest.Config) (kubernetes.Interface, error)
		wantErr    bool
	}{
		{
			name:       "Successful Client Creation",
			buildFunc:  mockBuildConfigSuccess,
			clientFunc: mockNewClientsetSuccess,
			wantErr:    false,
		},
		{
			name:       "Failed Config Creation",
			buildFunc:  mockBuildConfigFailure,
			clientFunc: mockNewClientsetSuccess,
			wantErr:    true,
		},
		{
			name:       "Failed Clientset Creation",
			buildFunc:  mockBuildConfigSuccess,
			clientFunc: mockNewClientsetFailure,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1, err := k8s.GetKubernetesClient(tt.buildFunc, tt.clientFunc)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetKubernetesClient() error = %v, wantErr %v", err, tt.wantErr)

				return
			}
			if tt.wantErr == false && !reflect.DeepEqual(got, &rest.Config{Host: "https://mock-cluster"}) {
				t.Errorf("GetKubernetesClient() got = %v, want %v", got, &rest.Config{Host: "https://mock-cluster"})
			}
			if tt.wantErr == false && got1 == nil {
				t.Errorf("GetKubernetesClient() got1 = nil, want non-nil clientset")
			}
		})
	}
}

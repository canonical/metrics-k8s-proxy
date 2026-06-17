package k8s

import (
	"os"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// GetKubernetesClient creates a Kubernetes clientset using in-cluster or kubeconfig setup.
func GetKubernetesClient(
	buildConfigFunc func() (*rest.Config, error),
	newClientsetFunc func(*rest.Config) (kubernetes.Interface, error),
) (*rest.Config, kubernetes.Interface, error) {
	config, err := buildConfigFunc()
	if err != nil {
		return nil, nil, err
	}

	clientset, err := newClientsetFunc(config)
	if err != nil {
		return nil, nil, err
	}

	return config, clientset, nil
}

// DefaultBuildConfigFunc is the default function to build a Kubernetes REST config when not testing.
func DefaultBuildConfigFunc() (*rest.Config, error) {
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}

	return rest.InClusterConfig()
}

func DefaultNewClientsetFunc(config *rest.Config) (kubernetes.Interface, error) {
	return kubernetes.NewForConfig(config)
}

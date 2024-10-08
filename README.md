# metrics-k8s-proxy

**Metrics K8s Proxy** is a lightweight proxy designed to expose a unified metrics endpoint for multiple Kubernetes pods. The proxy watches for pods in a Kubernetes cluster and listens on port `15090`, where it exposes aggregated metrics on a `/metrics` endpoint. 

The proxy itself doesn't scrape metrics. Instead, when Prometheus sends a scrape request, the proxy fans out the request to all registered endpoints that have Prometheus Kubernetes service discovery annotations. It then aggregates the results and appends metadata such as pod names and namespaces, allowing Prometheus to efficiently retrieve metrics from multiple pods.

This proxy was primarily designed for use with **Juju charms** as an add-on to the [Prometheus scrape library](https://charmhub.io/prometheus-k8s/libraries/prometheus_scrape). The library only accepts static endpoints in scrape jobs and doesn't support Kubernetes service discovery directly. The proxy solves this limitation by fanning out Prometheus scrape requests to all registered pod endpoints that have Prometheus service discovery annotations, aggregating the results, and appending pod-specific metadata such as names and namespaces.

While the main use case is for Juju charms, this proxy can be expanded to other scenarios where aggregating metrics from multiple Kubernetes pods into a single endpoint is beneficial.


## Features

- **Pod Discovery**: Watches for changes in the Kubernetes pods based on specified label selectors.
- **Aggregation**: Combines metrics from multiple pods and includes a health status indicator (`up` metric) for each pod. If a pod's metrics can't be retrieved, its `up` metric is set to `0`.
- **Exposes a unified `/metrics` endpoint**: You can access aggregated metrics for all watched pods on the proxy's `/metrics` HTTP endpoint.
- **Configurable via Command Line Arguments**:
  - `--labels`: Label selector for the pods to watch (e.g., `app=ztunnel`).
  - `--timeout`: Server read and write timeout (default is 9 seconds).

The design decision behind the default 9-second timeout is based on Prometheus' typical scrape interval of 10 seconds. This ensures that no single slow pod hangs the entire scrape request. The proxy fans out requests to all discovered pods in parallel, each within a configurable 9-second timeout. For any endpoint that fails to respond within this time, the `up` metric is set to `0` (indicating a metric collection failure), while successful responses from other pods are still aggregated and returned.


## Annotations

To enable scraping for a pod, the following Prometheus annotations should be added to your pod spec:
- `prometheus.io/scrape: "true"` - Enables scraping for the pod.
- `prometheus.io/port`: Port to scrape metrics from (default: 80).
- `prometheus.io/path`: Path for metrics (default: `/metrics`).

## Usage 

### Usage locally

To run the `metrics-k8s-proxy`, you need to specify the required label selector and optionally the server timeout.

```bash
# ensure port 15090 is empty on your host

export KUBECONFIG=<YOUR-KUBE-CONFIG>
./metrics-proxy --labels app=my-app --timeout 15s
curl http://<local-IP>:15090/metrics
```

### Usage in Kubernetes
You can deploy metrics-k8s-proxy in a Kubernetes cluster by creating a deployment manifest. Here is an example of a basic deployment YAML:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: metrics-k8s-proxy
spec:
  replicas: 1
  selector:
    matchLabels:
      app: metrics-k8s-proxy
  template:
    metadata:
      labels:
        app: metrics-k8s-proxy
    spec:
      containers:
      - name: metrics-k8s-proxy
        image: <your-registry>/<Proxy-OCI-Image>
        args:
        - --labels=app=my-app
        ports:
        - containerPort: 15090
```

The proxy exposes metrics on port `15090`. To retrieve the aggregated metrics from all scraped pods, you can query the `/metrics` endpoint:

`curl http://<metrics-k8s-proxy-IP>:15090/metrics`


### Usage in Juju

To integrate the proxy into your Juju charm, follow these summarized steps based on this [PR implementation](https://github.com/canonical/istio-k8s-operator/pull/20):

1. **Label your Kubernetes workloads**: Ensure that your workloads are labeled with a unique identifier that can be used by the proxy for discovery.

2. **Add a container for the proxy**: Create a separate container in your charm specifically for running the proxy.

3. **Set the container image**: Use the proxy image available through the proxy's [rock repository](https://github.com/canonical/metrics-proxy-rock).

4. **Configure a Pebble service**: Set up a Pebble service to run the proxy binary, passing the required label selectors and an optional custom timeout:
   ```python
   "command": f"metrics-proxy --labels {self._metrics_labels}"
   ```

5. **Instantiate the `MetricsEndpointProvider`**: Create a `MetricsEndpointProvider` instance with a job configuration pointing to the proxy's endpoint:
    ```python
    jobs=[{"static_configs": [{"targets": ["*:15090"]}]}]
    ```

## Build & Release

This project uses goreleaser to manage builds and releases.

In local development, you can build a snapshot release like so:

```shell
goreleaser --snapshot --rm-dist
```

The output will be present in `dist/`.

To create a release, create a new tag and push to Github, the release will be automatically
created by Goreleaser.

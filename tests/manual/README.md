### **Prerequisites**
- A Kubernetes cluster.
- `kubectl` installed and configured.
- Go environment setup to build the binary.
- Prometheus annotations on pods for scraping.
- Download the YAML file `test-pods.yaml` from the project repository (it includes namespaces and pod definitions for testing).

### **Test Guide**

#### 1. **Apply the YAML File for Pod Definitions**

The YAML file `test-pods.yaml` contains the definitions for 3 pods with Prometheus annotations for scraping. Follow these steps to apply the file:

1. Apply the YAML file to create the namespaces and pods:

   ```bash
   kubectl apply -f test-pods.yaml
   ```

   This will create the following resources:
   - **Pod 1**  with metrics exposed on port `8080` at `/metrics-1`.
   - **Pod 2**  with metrics exposed on port `8081` at `/metrics-2`.
   - **Pod 3**  with metrics exposed on port `8082` at `/metrics-3`, but simulating a 15-second delay before responding.

#### 2. **Build and Run `metrics-proxy`**

1. **Build the `metrics-proxy` binary**:

   If you haven't already built the `metrics-proxy` binary, run the following command in the directory where the Go source code resides:

   ```bash
   go build -o metrics-proxy <repo-path>/cmd/metrics-proxy
   ```

2. **Set the `KUBECONFIG` environment variable**:

   Ensure that the `KUBECONFIG` variable is set to point to your Kubernetes configuration file (usually located in `~/.kube/config`).

   ```bash
   export KUBECONFIG=<path-to-kubeconfig>
   ```

3. **Run the `metrics-proxy` application**:

   Start the `metrics-proxy` application, targeting pods with the label `foo=bar`:

   ```bash
   ./metrics-proxy --labels foo=bar
   ```

   The proxy should instatly report all pods in the cluster that have the `foo=bar` label and Prometheus annotation `scrape=true` with the below logs 

    ```bash
    2024/10/15 23:33:26 Updated pod pod-1 with IP <pod-ip>
    2024/10/15 23:33:26 Updated pod pod-2 with IP <pod-ip>
    2024/10/15 23:33:26 Updated pod pod-3 with IP <pod-ip>
    ```

#### 3. **Validate the Metrics**

1. **Check `metrics-proxy` output**:

   Once the `metrics-proxy` is running, it will scrape metrics from the 3 pods and combine them. You can view the collected metrics by curling the `/metrics` endpoint on `localhost:15090` 

   Use the following command:

   ```bash
   curl localhost:15090/metrics
   ```

2. **Expected Output**:

   - For **Pod 1** and **Pod 2**, you should immediately see metrics in the output, similar to the following:

     ```plaintext
        metric_total{k8s_pod_name="pod-2",k8s_namespace="namespace-a"} 123

        up{k8s_pod_name="pod-2",k8s_namespace="namespace-a"} 1

        metric_total{k8s_pod_name="pod-1",k8s_namespace="namespace-a"} 123

        up{k8s_pod_name="pod-1",k8s_namespace="namespace-a"} 1
     ```

   - **Pod 3** will delay its response by 15 seconds, once default `scrapeTimeout` (9 seconds), you should observe that the `up` metric for Pod 3 is set to `0`, indicating that it timed out:

     ```plaintext
     up{k8s_pod_name="pod-3",k8s_namespace="namespace-a"} 0
     ```
     An additional log can be observerd in the proxy

     ```bash
     2024/10/16 00:47:18 Error scraping http://<pod-ip>:8082/metrics-3: Get "http://<pod-ip>:8082/metrics-3": context deadline exceeded
     ```

3. **Verify Appended Labels**:

   In the combined metrics, ensure that the following extra labels are appended to each metric:
   - `k8s_pod_name`: The name of the pod.
   - `k8s_namespace`: The namespace the pod is running in.

   Example output with labels:

    ```plaintext
    metric_total{k8s_pod_name="pod-2",k8s_namespace="namespace-a"}
    up{k8s_pod_name="pod-2",k8s_namespace="namespace-a"}
    ```

#### 4. **Additional Testing**

- You can modify the timeout for the `metrics-proxy` via setting the `scrape_timeout` flag to observe the behavior when the timeout threshold is adjusted for delayed pods.
- Test scaling the number of pods or changing the labels to ensure the proxy correctly filters based on label selectors.

#### 5. **Clean Up**

Once testing is complete, delete the created namespaces and pods:

```bash
kubectl delete -f test-pods.yaml
```

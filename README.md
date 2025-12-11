# Far-Edge Kubelet
Far-edge Kubelet bridges Kubernetes with the Far-edge devices. It instantiates a virtual node in the Kubernetes cluster that is associated with the Far-edge device it is responsible for, and connects to the Kubernetes control plane through the Kubernetes API as a standard Kubelet. Following this strategy allows the Kuberentes control plane to look at the virtual nodes associated with Far-edge devices as regular nodes in the cluster and consider them for scheduling. The workload deployment commands are received by the Far-edge Kubelet, which downloads the Far-edge Kubelet workload from an OCI-compliant registry and deploys it in the Far-edge device by interacting with the NextGenGW.

## FITA Framework

The [Far-Edge Kubelet](https://github.com/fraunhoferportugal/far-edge-kubelet) is one of the core components of [FITA](https://github.com/fraunhoferportugal/fita).

For full documentation, updates, and project context, please visit the main [FITA](https://github.com/fraunhoferportugal/fita) repository.

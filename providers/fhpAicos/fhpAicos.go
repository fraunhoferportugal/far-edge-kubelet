package fhpAicos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"far-edge-kubelet/providers/fhpAicos/protocol"
	"far-edge-kubelet/providers/fhpAicos/protocol/leshan"
	"far-edge-kubelet/providers/fhpAicos/protocol/nextgengw"
	"far-edge-kubelet/providers/fhpAicos/registry"

	dto "github.com/prometheus/client_model/go"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	stats "github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// Provider configuration defaults.
	defaultCPUCapacity    = "1"
	defaultMemoryCapacity = "4096k"
	defaultPodCapacity    = "5"

	// Values used in tracing as attribute keys.
	namespaceKey     = "namespace"
	nameKey          = "name"
	containerNameKey = "containerName"

	// Annotation for identifying the image of the communication interface adapter
	adapterImageAnnotation = "communication-adapter.fita/image"
)

// Provider implements the virtual-kubelet provider interface and stores pods in memory.
type Provider struct { //nolint:golint
	nodeName           string
	namespace          string
	farEdgeNodeId      string
	operatingSystem    string
	kubernetesIP       string //this is the IP address of the pod where the Kubelet is running
	nodeIP             string //this is the real IP address of the node, if available
	daemonEndpointPort int32
	pods               map[string]ProviderPod
	config             ProviderConfig
	startTime          time.Time
	stats              *ProviderResourceStats
	notifier           func(*apiv1.Pod)
	mutex              sync.Mutex
}

// ProviderConfig contains a mock virtual-kubelet's configurable parameters.
type ProviderConfig struct { //nolint:golint
	CPU            string            `json:"cpu,omitempty"`
	CPUArch        string            `json:"cpuArch,omitempty"`
	CPUArchVariant string            `json:"cpuArchVariant,omitempty"`
	Memory         string            `json:"memory,omitempty"`
	Pods           string            `json:"pods,omitempty"`
	Others         map[string]string `json:"others,omitempty"`
	ExtraFlags     map[string]string `json:"extraflags,omitempty"`
	ProviderID     string            `json:"providerID,omitempty"`
	NodeArch       string            `json:"nodearch,omitempty"`

	//Far-Edge Servers
	LeshanEnabled bool
	LeshanIp      string
	LeshanPort    int

	NextGenGwEnabled bool
	NextGenGwIp      string
	NextGenGwPort    string

	//Far-Edge Registry
	FarEdgeRegistry      registry.FarEdgeRegistryConfig
	FarEdgeLocalRegistry string
}

type ProviderPod struct {
	Pod      *apiv1.Pod
	Adapter  *appsv1.Deployment
	Packages []ProviderPackage
}

type ProviderPackage struct {
	packageId   int
	packageFile string
	imageName   string
	stats       *ProviderResourceStats
}

type ProviderResourceStats struct {
	timestamp       time.Time
	cpuUsageSeconds float64
	memoryUsage     float64
}

func NewProviderConfig(config ProviderConfig, nodeName, operatingSystem string, internalIP string, daemonEndpointPort int32) (*Provider, error) {
	// set defaults
	if config.CPU == "" {
		config.CPU = defaultCPUCapacity
	}
	if config.Memory == "" {
		config.Memory = defaultMemoryCapacity
	}
	if config.Pods == "" {
		config.Pods = defaultPodCapacity
	}

	provider := Provider{
		nodeName:           nodeName,
		namespace:          os.Getenv("NAMESPACE"),
		farEdgeNodeId:      os.Getenv("NODE_ID"),
		operatingSystem:    operatingSystem,
		kubernetesIP:       internalIP,
		nodeIP:             internalIP,
		daemonEndpointPort: daemonEndpointPort,
		pods:               make(map[string]ProviderPod),
		config:             config,
		startTime:          time.Now(),
		stats:              &ProviderResourceStats{},
	}
	provider.stats.timestamp = time.Now()

	var err error
	var address string = ""

	if config.LeshanEnabled {
		address, err = leshan.GetNodeAddress(config.LeshanIp, config.LeshanPort, provider.farEdgeNodeId)
	} else {
		address, err = nextgengw.GetNodeAddress(provider.farEdgeNodeId)
	}

	if err == nil {
		provider.nodeIP = address
	}

	return &provider, nil
}

func NewFhPAICOSProvider(providerConfig, nodeName, operatingSystem string, internalIP string, daemonEndpointPort int32) (*Provider, error) {
	config, err := loadConfig(providerConfig, nodeName)
	if err != nil {
		return nil, err
	}

	return NewProviderConfig(config, nodeName, operatingSystem, internalIP, daemonEndpointPort)
}

// loadConfig loads the given json configuration files and parses environment variables
func loadConfig(providerConfig, nodeName string) (config ProviderConfig, err error) {
	//providerConfig is the JSON file path which may be used in the future

	//Set default values
	config.CPU = defaultCPUCapacity
	config.Memory = defaultMemoryCapacity
	config.Pods = defaultPodCapacity

	// Parse Node resource capabilities
	cpu := os.Getenv("NODE_CPU_CAP")
	if cpu != "" {
		config.CPU = cpu
	}
	mem := os.Getenv("NODE_MEM_CAP")
	if mem != "" {
		config.Memory = mem
	}
	pods := os.Getenv("NODE_POD_CAP")
	if pods != "" {
		config.Pods = pods
	}

	// Parse CPU data
	config.NodeArch = os.Getenv("NODE_ARCH")
	if config.NodeArch == "" {
		config.NodeArch = "arm-v7"
	}

	arch := strings.Split(config.NodeArch, "-")
	config.CPUArch = arch[0]
	if len(arch) > 1 {
		config.CPUArchVariant = arch[1]
	} else {
		config.CPUArchVariant = ""
	}

	// Parse Server configurations
	providerMode := os.Getenv("PROVIDER_MODE")
	if providerMode == "leshan" {
		config.LeshanEnabled = true
		config.LeshanIp = os.Getenv("LESHAN_IP")
		config.LeshanPort, _ = strconv.Atoi(os.Getenv("LESHAN_PORT"))
	} else {
		config.NextGenGwEnabled = true
		config.NextGenGwIp = os.Getenv("MQTT_BROKER_URI")
		config.NextGenGwPort = os.Getenv("MQTT_BROKER_PORT")
		nextgengw.Connect(nodeName, config.NextGenGwIp, config.NextGenGwPort)
	}

	// Parse Registry configurations
	overrideDefaultRegistry, err := strconv.ParseBool(os.Getenv("FAR_EDGE_REGISTRY_OVERRIDE_DEFAULT"))
	if err != nil {
		log.L.Info("Could't parse \"FAR_EDGE_REGISTRY_OVERRIDE_DEFAULT\" env variable, using false")
		overrideDefaultRegistry = false
	}

	overrideRegistry, err := strconv.ParseBool(os.Getenv("FAR_EDGE_REGISTRY_OVERRIDE"))
	if err != nil {
		log.L.Info("Could't parse \"FAR_EDGE_REGISTRY_OVERRIDE\" env variable, using false")
		overrideRegistry = false
	}

	plainHTTP, err := strconv.ParseBool(os.Getenv("FAR_EDGE_REGISTRY_PLAIN_HTTP"))
	if err != nil {
		log.L.Info("Could't parse \"FAR_EDGE_REGISTRY_PLAIN_HTTP\" env variable, using false")
		plainHTTP = false
	}

	insecure, err := strconv.ParseBool(os.Getenv("FAR_EDGE_REGISTRY_INSECURE"))
	if err != nil && !plainHTTP {
		log.L.Info("Could't parse \"FAR_EDGE_REGISTRY_INSECURE\" env variable, using false")
		insecure = false
	}

	if plainHTTP {
		insecure = true
	}

	config.FarEdgeRegistry = registry.FarEdgeRegistryConfig{
		Url:                     os.Getenv("FAR_EDGE_REGISTRY"),
		Username:                os.Getenv("FAR_EDGE_REGISTRY_USERNAME"),
		Password:                os.Getenv("FAR_EDGE_REGISTRY_PASSWORD"),
		PlainHTTP:               plainHTTP,
		InsecureSkipTLSVerify:   insecure,
		OverrideDefaultRegistry: overrideDefaultRegistry,
		OverrideRegistry:        overrideRegistry,
	}
	config.FarEdgeLocalRegistry = os.Getenv("FAR_EDGE_LOCAL_REGISTRY")

	// Parse Labels
	configFlags := make(map[string]string)
	configFlags["embserve"] = "true"

	caps := os.Getenv("NODE_CAPS")
	if caps != "" {
		for _, cap := range strings.Split(caps, ";") {
			configFlags[cap] = "true"
		}
	}

	config.ExtraFlags = configFlags

	if _, err = resource.ParseQuantity(config.CPU); err != nil {
		return config, fmt.Errorf("invalid CPU value %v", config.CPU)
	}
	if _, err = resource.ParseQuantity(config.Memory); err != nil {
		return config, fmt.Errorf("invalid memory value %v", config.Memory)
	}
	if _, err = resource.ParseQuantity(config.Pods); err != nil {
		return config, fmt.Errorf("invalid pods value %v", config.Pods)
	}
	for _, v := range config.Others {
		if _, err = resource.ParseQuantity(v); err != nil {
			return config, fmt.Errorf("invalid other value %v", v)
		}
	}
	return config, nil
}

func getReference[T any](elem T) *T {
	return &elem
}

func deployCommunicationInterfaceAdapter(ctx context.Context, provider *Provider, pod *apiv1.Pod) (string, error) {
	// Pull policy
	// Ports
	// Service Name
	image, exists := pod.Annotations[adapterImageAnnotation]
	log.G(ctx).Infof("Communication adapter image: %s", image)
	if !exists {
		return "", fmt.Errorf("no communication interface adapter image provided")
	}

	deploymentName := pod.Name + "-communication-adapter"
	adapterDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: pod.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{APIVersion: "v1", Kind: "Pod", Name: pod.Name, UID: pod.UID},
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: getReference(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": deploymentName,
				},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": deploymentName,
					},
				},
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{
							Name:            "adapter",
							Image:           image,
							ImagePullPolicy: apiv1.PullAlways,
							Ports: []apiv1.ContainerPort{
								{
									Name:          "http",
									Protocol:      apiv1.ProtocolTCP,
									ContainerPort: 8090,
								},
							},
							Env: []apiv1.EnvVar{
								{Name: "NODE_NAME", Value: provider.farEdgeNodeId},
								{Name: "MQTT_BROKER_URL", Value: fmt.Sprintf("mqtt://%s:%s", provider.config.NextGenGwIp, provider.config.NextGenGwPort)},
								{Name: "MQTT_CLIENT_NAME", Value: deploymentName},
								{Name: "SERVICE_NAME", Value: "Temperature"},
							},
						},
					},
				},
			},
		},
	}

	log.G(ctx).Infof("This is the far-edge-node id: &s", provider.farEdgeNodeId)

	config, err := rest.InClusterConfig()
	if err != nil {
		log.G(ctx).Errorf("error getting in cluster config: %v", err)
		return "", err
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.G(ctx).Errorf("error creating k8s client config: %v", err)
		return "", err
	}

	fieldManager, err := os.Hostname()
	if err != nil {
		fieldManager = "far-edge-kubelet-" + provider.nodeName
	}

	labelSelector := labels.NewSelector()
	requirement, err := labels.NewRequirement("app", selection.Equals, []string{deploymentName})
	if err != nil {
		log.G(ctx).Errorf("cannot create label selector: %v", err)
		return "", err
	}
	labelSelector = labelSelector.Add(*requirement)

	var adapterPods *apiv1.PodList
	adapterDeployment, err = clientset.AppsV1().Deployments(adapterDeployment.Namespace).Create(context.TODO(), adapterDeployment, metav1.CreateOptions{FieldManager: fieldManager})
	if k8serrors.IsAlreadyExists(err) {
		adapterPods, err = clientset.CoreV1().Pods(adapterDeployment.Namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: labelSelector.String()})
		if err != nil {
			log.G(ctx).Errorf("error getting adapter pods: %v", err)
			return "", err
		}
		if len(adapterPods.Items) > 0 {
			ip := adapterPods.Items[0].Status.PodIP
			log.G(ctx).Infof("Adapter pod IP: %s", ip)
			return ip, nil
		}
	} else if err != nil {
		log.G(ctx).Errorf("error creating adapter deployment: %v", err)
		return "", err
	}

	nameSelector := fields.OneTermEqualSelector("metadata.name", deploymentName)
	watchOptions := metav1.ListOptions{}
	if adapterPods != nil {
		watchOptions.ResourceVersion = adapterPods.ResourceVersion
	}
	watchOptions.FieldSelector = nameSelector.String()
	deploymentsWatch, err := clientset.AppsV1().Deployments(adapterDeployment.Namespace).Watch(context.TODO(), watchOptions)
	if err != nil {
		log.G(ctx).Errorf("error creating deployment watch: %v", err)
		return "", err
	}

	for event := range deploymentsWatch.ResultChan() {
		deployment, ok := event.Object.(*appsv1.Deployment)
		if !ok {
			log.G(ctx).Error("failed to cast event to deployment")
		}
		if deployment.Status.AvailableReplicas > 0 {
			deploymentsWatch.Stop()
			break
		}
	}

	adapterPods, err = clientset.CoreV1().Pods(adapterDeployment.Namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: labelSelector.String()})
	if err != nil {
		log.G(ctx).Errorf("error getting adapter pods: %v", err)
		return "", err
	}
	ip := adapterPods.Items[0].Status.PodIP
	log.G(ctx).Infof("Adapter pod IP: %s", ip)
	return ip, nil
}

// CreatePod accepts a Pod definition and stores it in memory.
func (p *Provider) CreatePod(ctx context.Context, pod *apiv1.Pod) error {
	ctx, span := trace.StartSpan(ctx, "CreatePod")
	defer span.End()
	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, pod.Namespace, nameKey, pod.Name)
	key, err := buildKey(pod)
	if err != nil {
		return err
	}

	p.mutex.Lock()
	log.G(ctx).Infof("Pod key %q", key)

	now := metav1.NewTime(time.Now())
	providerPod := ProviderPod{
		Pod: pod,
	}

	pod.Status = apiv1.PodStatus{
		HostIP:    p.kubernetesIP,
		PodIP:     p.kubernetesIP,
		StartTime: &now,
	}

	deployFailed := false
	for _, container := range pod.Spec.Containers {
		log.G(ctx).Infof("Container Name: %q", container.Name)
		log.G(ctx).Infof("Image Name: %q", container.Image)

		//Validate that the package is unique
		for _, pd := range p.pods {
			for _, pkg := range pd.Packages {
				if container.Image == pkg.imageName {
					log.G(ctx).Infof("embServe services must be unique. %q is already installed!", container.Image)
					deployFailed = true
				}
			}
		}

		if deployFailed {
			break
		}

		packageFile, err := registry.FetchPackage(ctx, p.config.FarEdgeRegistry, p.config.FarEdgeLocalRegistry, container.Image, p.config.CPUArch, p.config.CPUArchVariant, p.operatingSystem)
		if err != nil {
			deployFailed = true
			fmt.Println(err)
			log.G(ctx).Infof("Failed to fetch package: %q", container.Image)
			break
		}

		log.G(ctx).Infof("Image Fetched: %q", packageFile)

		packageId := 0

		if p.config.LeshanEnabled {
			packageId, err = leshan.DeployPackage(p.config.LeshanIp, p.config.LeshanPort, p.farEdgeNodeId, packageFile)

		} else {
			packageId, err = nextgengw.DeployPackage(p.farEdgeNodeId, packageFile)
		}
		if err != nil {
			log.G(ctx).Infof("Failed to deploy image with name: %q", container.Image)

			//Remove any leftover data
			if packageId != 0 {
				leshan.RemovePackage(p.config.LeshanIp, p.config.LeshanPort, p.farEdgeNodeId, packageId)
				log.G(ctx).Infof("Removed leftover data for image %q: %q", container.Image, packageId)
			}
			deployFailed = true
			break

		} else {
			log.G(ctx).Infof("Image with name %q deployed: %d", container.Image, packageId)

			// Add packageId to list
			providerPackage := ProviderPackage{
				packageId:   packageId,
				packageFile: packageFile,
				imageName:   container.Image,
				stats:       &ProviderResourceStats{},
			}
			providerPackage.stats.timestamp = time.Now()
			providerPod.Packages = append(providerPod.Packages, providerPackage)
		}
	}

	// Check if a communication interface adapter is required
	_, exists := pod.Annotations[adapterImageAnnotation]
	if exists {
		if !deployFailed {
			time.Sleep(1 * time.Second)
			log.G(ctx).Debug("Deploy communication interface adapter")
			ip, err := deployCommunicationInterfaceAdapter(ctx, p, pod)
			if err == nil {
				providerPod.Pod.Status.PodIP = ip
				providerPod.Pod.Status.PodIPs = []apiv1.PodIP{{IP: ip}}
			} else {
				deployFailed = true
			}
		}
	} else {
		log.G(ctx).Debug("No communication adapter requested")
	}

	if deployFailed {
		// Remove any leftover packages
		for _, providerPackage := range providerPod.Packages {
			err := error(nil)

			if p.config.LeshanEnabled {
				err = leshan.RemovePackage(p.config.LeshanIp, p.config.LeshanPort, p.farEdgeNodeId, providerPackage.packageId)
			} else {
				err = nextgengw.RemovePackage(p.farEdgeNodeId, providerPackage.packageId)
			}
			if err != nil {
				log.G(ctx).Infof("Failed to remove package %d from node %q in pod %q", providerPackage.packageId, p.farEdgeNodeId, pod.Name)
			} else {
				log.G(ctx).Infof("Removed package %q since we failed the deployment", providerPackage.packageId)
			}
		}

		p.mutex.Unlock()
		return errors.New("failed to create pod")
	}

	// Add services as annotations in Pod
	var serviceMetadata []map[string]interface{}
	for _, providerPackage := range providerPod.Packages {
		file, err := os.Open(providerPackage.packageFile)
		if err != nil {
			fmt.Printf("Skipping %s: %v\n", providerPackage.packageFile, err)
			continue
		}
		defer file.Close()

		bytes, err := io.ReadAll(file)
		if err != nil {
			fmt.Printf("Skipping %s due to read error: %v\n", providerPackage.packageFile, err)
			continue
		}

		// Parse service into a generic map
		var data map[string]interface{}
		if err := json.Unmarshal(bytes, &data); err != nil {
			fmt.Printf("Skipping %s due to unmarshal error: %v\n", providerPackage.packageFile, err)
			continue
		}

		// Add the packageId
		data["id"] = providerPackage.packageId

		// Remove the binary, not needed
		delete(data, "service")

		// Append the parsed JSON object to the array
		serviceMetadata = append(serviceMetadata, data)
	}

	serviceMetadataString, err := json.MarshalIndent(serviceMetadata, "", "  ")
	if err != nil {
		fmt.Printf("Failed to marshal JSON array: %v\n", err)
	} else {
		// Add the metadata as an annotation
		if pod.ObjectMeta.Annotations == nil {
			pod.ObjectMeta.Annotations = make(map[string]string)
		}

		pod.ObjectMeta.Annotations["embserve.fhp.pt/serviceMetadata"] = string(serviceMetadataString)
	}

	//Add embServe annotations
	pod.ObjectMeta.Annotations["embserve.fhp.pt/address"] = p.nodeIP
	pod.ObjectMeta.Annotations["embserve.fhp.pt/runtime"] = "embserve"
	pod.ObjectMeta.Annotations["embserve.fhp.pt/nodeId"] = p.farEdgeNodeId

	conditions := []apiv1.PodCondition{
		{
			Type:   apiv1.PodInitialized,
			Status: apiv1.ConditionTrue,
		},
		{
			Type:   apiv1.PodReady,
			Status: apiv1.ConditionTrue,
		},
		{
			Type:   apiv1.PodScheduled,
			Status: apiv1.ConditionTrue,
		},
	}

	pod.Status.Phase = apiv1.PodRunning
	pod.Status.Conditions = conditions

	for _, container := range pod.Spec.Containers {
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, apiv1.ContainerStatus{
			Name:         container.Name,
			Image:        container.Image,
			Ready:        true,
			RestartCount: 0,
			State: apiv1.ContainerState{
				Running: &apiv1.ContainerStateRunning{
					StartedAt: now,
				},
			},
		})
	}
	p.pods[key] = providerPod

	if !p.config.LeshanEnabled {
		nextgengw.PodRuntimeManagement(pod.ObjectMeta.Annotations["ExecutionQoSMetrics"], pod.Namespace, pod.Name)
	}

	p.notifier(providerPod.Pod)
	p.mutex.Unlock()
	return nil
}

// UpdatePod accepts a Pod definition and updates its reference.
func (p *Provider) UpdatePod(ctx context.Context, pod *apiv1.Pod) error {
	ctx, span := trace.StartSpan(ctx, "UpdatePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, pod.Namespace, nameKey, pod.Name)

	log.G(ctx).Infof("receive UpdatePod %q", pod.Name)

	key, err := buildKey(pod)
	if err != nil {
		return err
	}

	providerPod := p.pods[key]
	providerPod.Pod = pod

	p.pods[key] = providerPod
	p.notifier(providerPod.Pod)
	return nil
}

// DeletePod deletes the specified pod out of memory.
func (p *Provider) DeletePod(ctx context.Context, pod *apiv1.Pod) (err error) {
	ctx, span := trace.StartSpan(ctx, "DeletePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, pod.Namespace, nameKey, pod.Name)

	log.G(ctx).Infof("Inside virtual kubelet mock receive DeletePod %q", pod.Name)

	key, err := buildKey(pod)
	if err != nil {
		return err
	}

	log.G(ctx).Infof("Pod key %q", key)

	if _, exists := p.pods[key]; !exists {
		return errdefs.NotFound("pod not found")
	}
	p.mutex.Lock()

	for _, providerPackage := range p.pods[key].Packages {
		err := error(nil)

		if p.config.LeshanEnabled {
			err = leshan.RemovePackage(p.config.LeshanIp, p.config.LeshanPort, p.farEdgeNodeId, providerPackage.packageId)
		} else {
			err = nextgengw.RemovePackage(p.farEdgeNodeId, providerPackage.packageId)
		}

		if err != nil {
			log.G(ctx).Infof("Failed to remove package %d from node %q in pod %q", providerPackage.packageId, p.farEdgeNodeId, pod.Name)
		}
	}

	now := metav1.Now()
	delete(p.pods, key)

	pod.Status.Phase = apiv1.PodSucceeded
	pod.Status.Reason = "ProviderPodDeleted"

	for idx := range pod.Status.ContainerStatuses {
		pod.Status.ContainerStatuses[idx].Ready = false
		pod.Status.ContainerStatuses[idx].State = apiv1.ContainerState{
			Terminated: &apiv1.ContainerStateTerminated{
				Message:    "Provider terminated container upon deletion",
				FinishedAt: now,
				Reason:     "ProviderPodContainerDeleted",
				StartedAt:  pod.Status.ContainerStatuses[idx].State.Running.StartedAt,
			},
		}
	}

	p.notifier(pod)
	p.mutex.Unlock()
	return nil
}

// GetPod returns a pod by name that is stored in memory.
func (p *Provider) GetPod(ctx context.Context, namespace, name string) (pod *apiv1.Pod, err error) {
	ctx, span := trace.StartSpan(ctx, "GetPod")
	defer func() {
		span.SetStatus(err)
		span.End()
	}()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, namespace, nameKey, name)

	log.G(ctx).Infof("receive GetPod %q", name)

	key, err := buildKeyFromNames(namespace, name)
	if err != nil {
		return nil, err
	}

	if pod, ok := p.pods[key]; ok {
		return pod.Pod, nil
	}
	return nil, errdefs.NotFoundf("pod \"%s/%s\" is not known to the provider", namespace, name)
}

// GetContainerLogs retrieves the logs of a container by name from the provider.
func (p *Provider) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	ctx, span := trace.StartSpan(ctx, "GetContainerLogs")
	defer span.End()

	// Add pod and container attributes to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, namespace, nameKey, podName, containerNameKey, containerName)

	log.G(ctx).Infof("receive GetContainerLogs %q", podName)
	return io.NopCloser(strings.NewReader("")), nil
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) RunInContainer(ctx context.Context, namespace, name, container string, cmd []string, attach api.AttachIO) error {
	log.G(context.TODO()).Infof("receive ExecInContainer %q", container)
	return nil
}

// AttachToContainer attaches to the executing process of a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) AttachToContainer(ctx context.Context, namespace, name, container string, attach api.AttachIO) error {
	log.G(ctx).Infof("receive AttachToContainer %q", container)
	return nil
}

// GetPodStatus returns the status of a pod by name that is "running".
// returns nil if a pod by that name is not found.
func (p *Provider) GetPodStatus(ctx context.Context, namespace, name string) (*apiv1.PodStatus, error) {
	ctx, span := trace.StartSpan(ctx, "GetPodStatus")
	defer span.End()

	// Add namespace and name as attributes to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, namespace, nameKey, name)

	log.G(ctx).Infof("receive GetPodStatus %q", name)

	pod, err := p.GetPod(ctx, namespace, name)
	if err != nil {
		return nil, err
	}

	return &pod.Status, nil
}

// GetPods returns a list of all pods known to be "running".
func (p *Provider) GetPods(ctx context.Context) ([]*apiv1.Pod, error) {
	ctx, span := trace.StartSpan(ctx, "GetPods")
	defer span.End()

	log.G(ctx).Info("receive GetPods")

	var pods []*apiv1.Pod

	for _, pod := range p.pods {
		pods = append(pods, pod.Pod)
	}

	return pods, nil
}

func (p *Provider) ConfigureNode(ctx context.Context, n *apiv1.Node) { //nolint:golint
	ctx, span := trace.StartSpan(ctx, "mock.ConfigureNode") //nolint:staticcheck,ineffassign
	defer span.End()

	log.G(ctx).Info("receive ConfigureNode")

	if p.config.ProviderID != "" {
		n.Spec.ProviderID = p.config.ProviderID
	}
	n.Status.Capacity = p.capacity()
	n.Status.Allocatable = p.capacity()
	n.Status.Conditions = p.nodeConditions()
	n.Status.Addresses = p.nodeAddresses()
	n.Status.DaemonEndpoints = p.nodeDaemonEndpoints()
	os := p.operatingSystem
	if os == "" {
		os = "zephyr"
	}
	n.Status.NodeInfo.OperatingSystem = os
	n.Status.NodeInfo.Architecture = p.config.NodeArch

	if n.ObjectMeta.Labels == nil {
		n.ObjectMeta.Labels = make(map[string]string)
	}
	n.ObjectMeta.Labels["alpha.service-controller.kubernetes.io/exclude-balancer"] = "true"
	n.ObjectMeta.Labels["node.kubernetes.io/exclude-from-external-load-balancers"] = "true"

	if n.ObjectMeta.Annotations == nil {
		n.ObjectMeta.Annotations = make(map[string]string)
	}
	n.ObjectMeta.Annotations["embserve.fhp.pt/address"] = p.nodeIP
	n.ObjectMeta.Annotations["embserve.fhp.pt/runtime"] = "embserve"
	n.ObjectMeta.Annotations["embserve.fhp.pt/nodeId"] = p.farEdgeNodeId
	n.ObjectMeta.Annotations["embserve.fhp.pt/namespace"] = p.namespace

	for key, value := range p.config.ExtraFlags {
		fmt.Println(key, value)
		n.ObjectMeta.Labels["extra.resources.fhp/"+key] = value
	}
}

// Capacity returns a resource list containing the capacity limits.
func (p *Provider) capacity() apiv1.ResourceList {
	rl := apiv1.ResourceList{
		"cpu":    resource.MustParse(p.config.CPU),
		"memory": resource.MustParse(p.config.Memory),
		"pods":   resource.MustParse(p.config.Pods),
	}
	for k, v := range p.config.Others {
		rl[apiv1.ResourceName(k)] = resource.MustParse(v)
	}
	return rl
}

// NodeConditions returns a list of conditions (Ready, OutOfDisk, etc), for updates to the node status
// within Kubernetes.
func (p *Provider) nodeConditions() []apiv1.NodeCondition {
	// TODO: Make this configurable
	return []apiv1.NodeCondition{
		{
			Type:               "Ready",
			Status:             apiv1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletPending",
			Message:            "kubelet is pending.",
		},
		{
			Type:               "OutOfDisk",
			Status:             apiv1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientDisk",
			Message:            "kubelet has sufficient disk space available",
		},
		{
			Type:               "MemoryPressure",
			Status:             apiv1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientMemory",
			Message:            "kubelet has sufficient memory available",
		},
		{
			Type:               "DiskPressure",
			Status:             apiv1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoDiskPressure",
			Message:            "kubelet has no disk pressure",
		},
		{
			Type:               "NetworkUnavailable",
			Status:             apiv1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "RouteController created a route",
		},
	}

}

// NodeAddresses returns a list of addresses for the node status
// within Kubernetes.
func (p *Provider) nodeAddresses() []apiv1.NodeAddress {
	return []apiv1.NodeAddress{
		{
			Type:    "InternalIP",
			Address: p.kubernetesIP,
		},
	}
}

// NodeDaemonEndpoints returns NodeDaemonEndpoints for the node status
// within Kubernetes.
func (p *Provider) nodeDaemonEndpoints() apiv1.NodeDaemonEndpoints {
	return apiv1.NodeDaemonEndpoints{
		KubeletEndpoint: apiv1.DaemonEndpoint{
			Port: p.daemonEndpointPort,
		},
	}
}

func (p *Provider) GetStatsSummary(ctx context.Context) (*stats.Summary, error) {
	var span trace.Span
	ctx, span = trace.StartSpan(ctx, "GetStatsSummary") //nolint: ineffassign,staticcheck
	defer span.End()

	log.G(ctx).Infof("receive GetStatsSummary")

	// Grab the current timestamp so we can report it as the time the stats were generated.
	time := metav1.NewTime(time.Now())

	// Create the Summary object that will later be populated with node and pod stats.
	res := &stats.Summary{}

	// Populate the Summary object with basic node stats.
	nodeUsageNanoCores := uint64(p.stats.cpuUsageSeconds * 1e9)
	nodeUsageBytes := uint64(p.stats.memoryUsage)

	res.Node = stats.NodeStats{
		NodeName:  p.nodeName,
		StartTime: metav1.NewTime(p.startTime),
		CPU: &stats.CPUStats{
			Time:           time,
			UsageNanoCores: &nodeUsageNanoCores,
		},
		Memory: &stats.MemoryStats{
			Time:       time,
			UsageBytes: &nodeUsageBytes,
		},
	}

	// Populate the Summary object with dummy stats for each pod known by this provider.
	for _, pod := range p.pods {
		var (
			// totalUsageNanoCores will be populated with the sum of the values of UsageNanoCores computes across all containers in the pod.
			totalUsageNanoCores uint64
			// totalUsageBytes will be populated with the sum of the values of UsageBytes computed across all containers in the pod.
			totalUsageBytes uint64
		)

		// Create a PodStats object to populate with pod stats.
		pss := stats.PodStats{
			PodRef: stats.PodReference{
				Name:      pod.Pod.Name,
				Namespace: pod.Pod.Namespace,
				UID:       string(pod.Pod.UID),
			},
			StartTime: pod.Pod.CreationTimestamp,
		}

		// Iterate over all containers in the current pod to compute stats
		for i, container := range pod.Pod.Spec.Containers {
			containerUsageNanoCores := uint64(pod.Packages[i].stats.cpuUsageSeconds * 1e9)
			totalUsageNanoCores += containerUsageNanoCores

			containerUsageBytes := uint64(pod.Packages[i].stats.memoryUsage)
			totalUsageBytes += containerUsageBytes

			// Append a ContainerStats object containing the dummy stats to the PodStats object.
			pss.Containers = append(pss.Containers, stats.ContainerStats{
				Name:      container.Name,
				StartTime: pod.Pod.CreationTimestamp,
				CPU: &stats.CPUStats{
					Time:           time,
					UsageNanoCores: &containerUsageNanoCores,
				},
				Memory: &stats.MemoryStats{
					Time:       time,
					UsageBytes: &containerUsageBytes,
				},
			})
		}

		// Populate the CPU and RAM stats for the pod and append the PodsStats object to the Summary object to be returned.
		pss.CPU = &stats.CPUStats{
			Time:           time,
			UsageNanoCores: &totalUsageNanoCores,
		}
		pss.Memory = &stats.MemoryStats{
			Time:       time,
			UsageBytes: &totalUsageBytes,
		}
		res.Pods = append(res.Pods, pss)
	}

	// Return the stats.
	return res, nil
}

func (p *Provider) GetMetricsResource(ctx context.Context) ([]*dto.MetricFamily, error) {
	var span trace.Span
	ctx, span = trace.StartSpan(ctx, "GetMetricsResource") //nolint: ineffassign,staticcheck
	defer span.End()

	log.G(ctx).Infof("receive GetMetricsResource")

	var (
		podNameStr       = "pod"
		containerNameStr = "container"
		namespaceNameStr = "namespace"

		containerMemoryMetricStr = "container_memory_working_set_bytes"
		podMemoryMetricStr       = "pod_memory_working_set_bytes"
		nodeMemoryMetricStr      = "node_memory_working_set_bytes"

		containerCpuMetricStr = "container_cpu_usage_seconds_total"
		podCpuMetricStr       = "pod_cpu_usage_seconds_total"
		nodeCpuMetricStr      = "node_cpu_usage_seconds_total"

		counterMetricType = dto.MetricType_COUNTER
		gaugeMetricType   = dto.MetricType_GAUGE
	)

	timestamp := time.Now()
	timestampMs := timestamp.UnixMilli()

	res := []*dto.MetricFamily{}

	for _, pod := range p.pods {
		var podCpuUsage float64 = 0.0
		var podMemoryUsage float64 = 0.0
		var err error

		for i, providerPackage := range pod.Packages {
			stats := protocol.ResourceStatistics{}

			if p.config.LeshanEnabled {
				stats, err = leshan.GetPackageStats(p.config.LeshanIp, p.config.LeshanPort, p.farEdgeNodeId, providerPackage.packageId)

				if err != nil {
					log.G(ctx).Infof("Failed to get stats for package %d from node %q in pod %q", providerPackage.packageId, p.farEdgeNodeId, pod.Pod.Name)
					continue
				}

			} else {
				//NextGenGw
				//TODO: Workaround since nextgengw lib is not thread safe. Remove when reworked
				p.mutex.Lock()
				stats, err = nextgengw.GetPackageStats(p.farEdgeNodeId, providerPackage.packageId)
				p.mutex.Unlock()

				if err != nil {
					log.G(ctx).Infof("Failed to get stats for package %d from node %q in pod %q", providerPackage.packageId, p.farEdgeNodeId, pod.Pod.Name)
					continue
				}
			}

			//This ensures all "containers" will report at least 1m CPU usage
			if stats.CpuUsage <= 0.001 {
				stats.CpuUsage = 0.001
			}

			//Store collected stats
			providerPackage.stats.memoryUsage = float64(stats.MemoryUsage)

			//Device will report CPU usage in %. So, we estimate CPU usage in seconds by getting
			//the delta time and multiplying by the CPU usage %. Should be enough for now.
			providerPackage.stats.cpuUsageSeconds += stats.CpuUsage / 100 * float64(timestampMs-providerPackage.stats.timestamp.UnixMilli()) / 1000
			providerPackage.stats.timestamp = timestamp

			//Add "container" memory usage to pod memory usage
			podMemoryUsage += providerPackage.stats.memoryUsage
			podCpuUsage += providerPackage.stats.cpuUsageSeconds

			//Add Container Memory metric
			memoryMetric := dto.Metric{
				Label: []*dto.LabelPair{
					{
						Name:  &namespaceNameStr,
						Value: &pod.Pod.ObjectMeta.Namespace,
					},
					{
						Name:  &podNameStr,
						Value: &pod.Pod.Name,
					},
					{
						Name: &containerNameStr,
						//NOTE: Our Packages array should have the same sequence as the Containers array
						Value: &pod.Pod.Spec.Containers[i].Name,
					},
				},
				Gauge: &dto.Gauge{
					Value: &providerPackage.stats.memoryUsage,
				},
				TimestampMs: &timestampMs,
			}
			memoryMetricFamily := dto.MetricFamily{
				Name:   &containerMemoryMetricStr,
				Type:   &gaugeMetricType,
				Metric: []*dto.Metric{&memoryMetric},
			}
			res = append(res, &memoryMetricFamily)

			//Add Container CPU metric
			cpuMetric := dto.Metric{
				Label: []*dto.LabelPair{
					{
						Name:  &namespaceNameStr,
						Value: &pod.Pod.ObjectMeta.Namespace,
					},
					{
						Name:  &podNameStr,
						Value: &pod.Pod.Name,
					},
					{
						Name: &containerNameStr,
						//NOTE: Our Packages array should have the same sequence as the Containers array
						Value: &pod.Pod.Spec.Containers[i].Name,
					},
				},
				Counter: &dto.Counter{
					Value: &providerPackage.stats.cpuUsageSeconds,
				},
				TimestampMs: &timestampMs,
			}
			cpuMetricFamily := dto.MetricFamily{
				Name:   &containerCpuMetricStr,
				Type:   &counterMetricType,
				Metric: []*dto.Metric{&cpuMetric},
			}
			res = append(res, &cpuMetricFamily)
		}

		//Add Pod Memory metric
		memoryMetric := dto.Metric{
			Label: []*dto.LabelPair{
				{
					Name:  &namespaceNameStr,
					Value: &pod.Pod.ObjectMeta.Namespace,
				},
				{
					Name:  &podNameStr,
					Value: &pod.Pod.Name,
				},
			},
			Gauge: &dto.Gauge{
				Value: &podMemoryUsage,
			},
			TimestampMs: &timestampMs,
		}
		memoryMetricFamily := dto.MetricFamily{
			Name:   &podMemoryMetricStr,
			Type:   &gaugeMetricType,
			Metric: []*dto.Metric{&memoryMetric},
		}
		res = append(res, &memoryMetricFamily)

		//Add Pod CPU metric
		cpuMetric := dto.Metric{
			Label: []*dto.LabelPair{
				{
					Name:  &namespaceNameStr,
					Value: &pod.Pod.ObjectMeta.Namespace,
				},
				{
					Name:  &podNameStr,
					Value: &pod.Pod.Name,
				},
			},
			Counter: &dto.Counter{
				Value: &podCpuUsage,
			},
			TimestampMs: &timestampMs,
		}
		cpuMetricFamily := dto.MetricFamily{
			Name:   &podCpuMetricStr,
			Type:   &counterMetricType,
			Metric: []*dto.Metric{&cpuMetric},
		}
		res = append(res, &cpuMetricFamily)
	}

	var err error
	var stats protocol.ResourceStatistics

	if p.config.LeshanEnabled {
		stats, err = leshan.GetNodeStats(p.config.LeshanIp, p.config.LeshanPort, p.farEdgeNodeId)

		if err != nil {
			log.G(ctx).Infof("Failed to get stats for node %q", p.farEdgeNodeId)
			return res, fmt.Errorf("failed to get stats for node %q", p.farEdgeNodeId)
		}

	} else {
		//NextGenGw
		//TODO: Workaround since nextgengw lib is not thread safe. Remove when reworked
		p.mutex.Lock()
		stats, err = nextgengw.GetNodeStats(p.farEdgeNodeId)
		p.mutex.Unlock()

		if err != nil {
			log.G(ctx).Infof("Failed to get stats for node %q", p.farEdgeNodeId)
			return res, fmt.Errorf("failed to get stats for node %q", p.farEdgeNodeId)
		}
	}

	//Store collected stats
	p.stats.memoryUsage = float64(stats.MemoryUsage)

	//Device will report CPU usage in %. So, we estimate CPU usage in seconds by getting
	//the delta time and multiplying by the CPU usage %. Should be enough for now.
	p.stats.cpuUsageSeconds += stats.CpuUsage / 100 * float64(timestampMs-p.stats.timestamp.UnixMilli()) / 1000
	p.stats.timestamp = timestamp

	//Add Node Memory metric
	memoryMetric := dto.Metric{
		Label: []*dto.LabelPair{},
		Gauge: &dto.Gauge{
			Value: &p.stats.memoryUsage,
		},
		TimestampMs: &timestampMs,
	}
	memoryMetricFamily := dto.MetricFamily{
		Name:   &nodeMemoryMetricStr,
		Type:   &gaugeMetricType,
		Metric: []*dto.Metric{&memoryMetric},
	}
	res = append(res, &memoryMetricFamily)

	//Add Node CPU metric
	cpuMetric := dto.Metric{
		Label: []*dto.LabelPair{},
		Counter: &dto.Counter{
			Value: &p.stats.cpuUsageSeconds,
		},
		TimestampMs: &timestampMs,
	}
	cpuMetricFamily := dto.MetricFamily{
		Name:   &nodeCpuMetricStr,
		Type:   &counterMetricType,
		Metric: []*dto.Metric{&cpuMetric},
	}
	res = append(res, &cpuMetricFamily)

	// fmt.Println(res)
	return res, nil
}

// NotifyPods is called to set a pod notifier callback function. This should be called before any operations are done
// within the provider.
func (p *Provider) NotifyPods(ctx context.Context, notifier func(*apiv1.Pod)) {
	p.notifier = notifier
}

func (p *Provider) PortForward(ctx context.Context, namespace, pod string, port int32, stream io.ReadWriteCloser) error {
	key, err := buildKeyFromNames(namespace, pod)
	if err != nil {
		return err
	}
	podObj := p.pods[key].Pod
	address := net.JoinHostPort(podObj.Status.PodIP, strconv.Itoa(int(port)))

	log.G(ctx).Infof("Forwarding pod %s to communication adapter pod: %s", pod, address)
	connection, err := net.DialTCP("tcp", nil, &net.TCPAddr{
		IP:   net.ParseIP(podObj.Status.PodIP),
		Port: int(port)})
	if err != nil {
		log.G(ctx).Errorf("cannot establish connection with adapter pod: %w", err)
		return err
	}
	defer stream.Close()
	defer connection.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(connection, stream)
		connection.Close()
	}()

	go func() {
		defer wg.Done()
		io.Copy(stream, connection)
		stream.Close()
	}()

	wg.Wait()

	return nil
}

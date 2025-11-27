package leshan

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"far-edge-kubelet/providers/fhpAicos/protocol"
)

type Node struct {
	Endpoint         string `json:"endpoint"`
	RegistrationID   string `json:"registrationId"`
	RegistrationDate int64  `json:"registrationDate"`
	LastUpdate       int64  `json:"lastUpdate"`
	Address          string `json:"address"`
	LwM2MVersion     string `json:"lwM2mVersion"`
	Lifetime         int    `json:"lifetime"`
	BindingMode      string `json:"bindingMode"`
	RootPath         string `json:"rootPath"`
	// ObjectLinks     []ObjectLink    `json:"objectLinks"`
	Secure                           bool                   `json:"secure"`
	AdditionalRegistrationAttributes map[string]interface{} `json:"additionalRegistrationAttributes"`
	QueueMode                        bool                   `json:"queuemode"`
	AvailableInstances               map[string][]int       `json:"availableInstances"`
}

type ApiResponse struct {
	Status  string  `json:"status"`
	Valid   bool    `json:"valid"`
	Success bool    `json:"success"`
	Failure bool    `json:"true"`
	Content Content `json:"content"`
}

type Content struct {
	Kind      string     `json:"kind"`
	Resources []Resource `json:"resources"`
	Id        int        `json:"id"`
}

type Resource struct {
	Kind   string                 `json:"kind"`
	Id     int                    `json:"id"`
	Type   string                 `json:"type"`
	Value  interface{}            `json:"value,omitempty"`  // Use an interface{} to handle varying value types
	Values map[string]interface{} `json:"values,omitempty"` // For multiResource
}

type ObjectLink struct {
	ObjectId         int  `json:"objectId"`
	ObjectInstanceId int  `json:"objectInstanceId"`
	NullLink         bool `json:"nullLink"`
}

func GetNodeAddress(serverUrl string, serverPort int, nodeId string) (string, error) {
	nodeUrl := fmt.Sprintf("http://%s:%d/api/clients/%s?format=TLV", serverUrl, serverPort, nodeId)
	resp, err := http.Get(nodeUrl)
	if err != nil {
		fmt.Println("Error getting node data", err)
		return "", err
	}
	defer resp.Body.Close()

	var node Node
	if err := json.NewDecoder(resp.Body).Decode(&node); err != nil {
		return "", fmt.Errorf("error parsing node data: %w", err)
	}

	// Check if the Connectivity Monitoring instance exists
	_, exists := node.AvailableInstances["4"]
	if !exists {
		return "", errors.New("failed to find connectivity monitoring object")
	}

	instanceUrl := fmt.Sprintf("http://%s:%d/api/clients/%s/4/0?format=TLV", serverUrl, serverPort, nodeId)
	resp, err = http.Get(instanceUrl)
	if err != nil {
		return "", fmt.Errorf("error getting instance data: %w", err)
	}
	defer resp.Body.Close()

	var apiResponse ApiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return "", fmt.Errorf("error parsing instance data: %w", err)
	}

	for _, resource := range apiResponse.Content.Resources {
		//IP Addresses Resource
		if resource.Id == 4 {
			for _, val := range resource.Values {
				if ip, ok := val.(string); ok {
					return ip, nil
				}
			}
		}
	}

	return "", fmt.Errorf("error getting instance data: %w", err)
}

func DeployPackage(serverUrl string, serverPort int, nodeId string, packageFile string) (int, error) {
	deviceUrl := "http://" + serverUrl + ":" + strconv.Itoa(serverPort) + "/api/clients/" + nodeId

	// Create Service ID
	resp, err := http.Get(deviceUrl)
	if err != nil {
		fmt.Println("Error getting node data", err)
		return 0, err
	}
	defer resp.Body.Close()

	var node Node
	err = json.NewDecoder(resp.Body).Decode(&node)
	if err != nil {
		fmt.Println("Error parsing node data", err)
		return 0, err
	}

	packageId := 1000

	_, exists := node.AvailableInstances["9"]
	if exists {
		for true {
			inUse := false

			for _, instance := range node.AvailableInstances["9"] {
				if instance == packageId {
					inUse = true
					packageId++
					break
				}

			}

			if !inUse {
				break
			}
		}
	}

	fmt.Println("packageId: " + strconv.Itoa(packageId))

	//Create Software Management instance
	data := `{"id":` + strconv.Itoa(packageId) + `, "kind": "instance", "resources": []}`
	body := bytes.NewBuffer([]byte(data))
	resp, err = http.Post(deviceUrl+"/9?timeout=5&format=TLV", "application/json", body)
	if err != nil {
		fmt.Println("Error making POST request:", err)
		return 0, err
	}
	defer resp.Body.Close()

	var response ApiResponse
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		return 0, err
	}

	if !response.Success {
		return 0, errors.New("request failed")
	}

	//Load package and send it to the Software Management instance
	serviceData, err := os.ReadFile(packageFile)
	if err != nil {
		return packageId, err
	}

	serviceDataString := hex.EncodeToString(serviceData)
	data = `{"id": 2, "kind": "singleResource", "value": "` + serviceDataString + `", "type": "opaque"}`
	body = bytes.NewBuffer([]byte(data))

	request, err := http.NewRequest("PUT", deviceUrl + "/9/" + strconv.Itoa(packageId) + "/2?timeout=300&format=TLV", body)
	if err != nil {
		fmt.Println("Error making POST request:", err)
		return packageId, err
	}
	request.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err = client.Do(request)
	if err != nil {
		fmt.Println("Error making PUT request:", err)
		return packageId, err
	}
	defer resp.Body.Close()

	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		fmt.Println("Error decoding response:", err)
		fmt.Println(resp)
		return packageId, err
	}

	if !response.Success {
		return packageId, errors.New("request failed")
	}

	//TODO: Check if this is really needed
	time.Sleep(1 * time.Millisecond)

	//Install package
	resp, err = http.Post(deviceUrl+"/9/"+strconv.Itoa(packageId)+"/4?timeout=5&format=TLV", "application/json", nil)
	if err != nil {
		fmt.Println("Error making POST request:", err)
		return packageId, err
	}
	defer resp.Body.Close()

	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		return packageId, err
	}

	if !response.Success {
		return packageId, errors.New("request failed")
	}

	//Activate package
	resp, err = http.Post(deviceUrl+"/9/"+strconv.Itoa(packageId)+"/10?timeout=5&format=TLV", "application/json", nil)
	if err != nil {
		fmt.Println("Error making POST request:", err)
		return packageId, err
	}
	defer resp.Body.Close()

	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		return packageId, err
	}

	if !response.Success {
		return packageId, errors.New("request failed")
	}

	return packageId, nil
}

func RemovePackage(serverUrl string, serverPort int, nodeId string, packageId int) error {
	deviceUrl := "http://" + serverUrl + ":" + strconv.Itoa(serverPort) + "/api/clients/" + nodeId

	// Delete package
	request, err := http.NewRequest("DELETE", deviceUrl+"/9/"+strconv.Itoa(packageId)+"?timeout=5", nil)
	if err != nil {
		fmt.Println("Error making POST request:", err)
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(request)
	if err != nil {
		fmt.Println("Error making PUT request:", err)
		return err
	}
	defer resp.Body.Close()

	var response ApiResponse
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		fmt.Println("Error decoding response:", err)
		fmt.Println(resp)
		return err
	}

	if !response.Success {
		return errors.New("request failed")
	}

	//NOTE: Leshan takes some time to reflect the removal of the instance
	//This leads to some edge cases. Wait a bit here to ensure everything is clean.
	time.Sleep(1 * time.Second)
	return nil
}

func DecodeValue(data map[string]interface{}, out interface{}) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, out)
}

func GetNodeStats(serverUrl string, serverPort int, nodeId string) (protocol.ResourceStatistics, error) {
	stats := protocol.ResourceStatistics{}

	nodeUrl := fmt.Sprintf("http://%s:%d/api/clients/%s?format=TLV", serverUrl, serverPort, nodeId)

	resp, err := http.Get(nodeUrl)
	if err != nil {
		fmt.Println("Error getting node data", err)
		return stats, err
	}
	defer resp.Body.Close()

	var node Node
	if err := json.NewDecoder(resp.Body).Decode(&node); err != nil {
		return stats, fmt.Errorf("error parsing node data: %w", err)
	}

	// Check if the Software Package Monitoring instance exists
	instances, exists := node.AvailableInstances["35003"]
	if !exists {
		return stats, errors.New("failed to find stats object")
	}

	//Iterate over all Software Package Monitoring instance
	for _, instance := range instances {
		instanceUrl := fmt.Sprintf("http://%s:%d/api/clients/%s/35003/%d?format=TLV", serverUrl, serverPort, nodeId, instance)

		resp, err = http.Get(instanceUrl)
		if err != nil {
			return stats, fmt.Errorf("error getting instance data: %w", err)
		}
		defer resp.Body.Close()

		var apiResponse ApiResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
			return stats, fmt.Errorf("error parsing instance data: %w", err)
		}

		// Check if the instance matches the package ID
		matchesPackage := false
		for _, resource := range apiResponse.Content.Resources {
			//27400 is an Objlnk which points to the corresponding Object
			if resource.Id == 27400 {
				//Get the resource as a ObjectLink. Value depends on the type of the resource
				var objectLink ObjectLink
				if valueMap, ok := resource.Value.(map[string]interface{}); ok {
					if err := DecodeValue(valueMap, &objectLink); err == nil {
						//Check if this monitoring instance is for the Device Object
						if objectLink.ObjectId == 3 && objectLink.ObjectInstanceId == 0 {
							matchesPackage = true
							break
						}
					}
				}
			}
		}

		// Collect statistics if package matches
		if matchesPackage {
			for _, resource := range apiResponse.Content.Resources {
				switch resource.Id {
				case 27401:
					if val, ok := resource.Value.(string); ok {
						stats.CpuUsage, _ = strconv.ParseFloat(val, 64)
					}
				case 27402:
					if val, ok := resource.Value.(string); ok {
						stats.MemoryUsage, _ = strconv.ParseInt(val, 10, 64)
					}
				case 27403:
					if val, ok := resource.Value.(string); ok {
						stats.Uptime, _ = strconv.ParseUint(val, 10, 64)
					}
				case 27404:
					if val, ok := resource.Value.(string); ok {
						stats.Error, _ = strconv.ParseInt(val, 10, 32)
					}
				}
			}
			return stats, nil
		}
	}
	return stats, errors.New("failed to find node stats object")
}

func GetPackageStats(serverUrl string, serverPort int, nodeId string, packageId int) (protocol.ResourceStatistics, error) {
	stats := protocol.ResourceStatistics{}

	nodeUrl := fmt.Sprintf("http://%s:%d/api/clients/%s?format=TLV", serverUrl, serverPort, nodeId)

	resp, err := http.Get(nodeUrl)
	if err != nil {
		fmt.Println("Error getting node data", err)
		return stats, err
	}
	defer resp.Body.Close()

	var node Node
	if err := json.NewDecoder(resp.Body).Decode(&node); err != nil {
		return stats, fmt.Errorf("error parsing node data: %w", err)
	}

	// Check if the Software Package Monitoring instance exists
	instances, exists := node.AvailableInstances["35003"]
	if !exists {
		return stats, errors.New("failed to find stats object")
	}

	//Iterate over all Software Package Monitoring instance
	for _, instance := range instances {
		instanceUrl := fmt.Sprintf("http://%s:%d/api/clients/%s/35003/%d?format=TLV", serverUrl, serverPort, nodeId, instance)

		resp, err = http.Get(instanceUrl)
		if err != nil {
			return stats, fmt.Errorf("error getting instance data: %w", err)
		}
		defer resp.Body.Close()

		var apiResponse ApiResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
			return stats, fmt.Errorf("error parsing instance data: %w", err)
		}

		// Check if the instance matches the package ID
		matchesPackage := false
		for _, resource := range apiResponse.Content.Resources {
			//27400 is an Objlnk which points to the corresponding Software Package
			if resource.Id == 27400 {
				//Get the resource as a ObjectLink. Value depends on the type of the resource
				var objectLink ObjectLink
				if valueMap, ok := resource.Value.(map[string]interface{}); ok {
					if err := DecodeValue(valueMap, &objectLink); err == nil {
						//Check if this monitoring instance is for the passed package
						if objectLink.ObjectId == 9 && objectLink.ObjectInstanceId == packageId {
							matchesPackage = true
							break
						}
					}
				}
			}
		}

		// Collect statistics if package matches
		if matchesPackage {
			for _, resource := range apiResponse.Content.Resources {
				switch resource.Id {
				case 27401:
					if val, ok := resource.Value.(string); ok {
						stats.CpuUsage, _ = strconv.ParseFloat(val, 64)
					}
				case 27402:
					if val, ok := resource.Value.(string); ok {
						stats.MemoryUsage, _ = strconv.ParseInt(val, 10, 64)
					}
				case 27403:
					if val, ok := resource.Value.(string); ok {
						stats.Uptime, _ = strconv.ParseUint(val, 10, 64)
					}
				case 27404:
					if val, ok := resource.Value.(string); ok {
						stats.Error, _ = strconv.ParseInt(val, 10, 32)
					}
				}
			}
			return stats, nil
		}
	}
	return stats, errors.New("failed to find stats object")
}

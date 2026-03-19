package nextgengw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"far-edge-kubelet/providers/fhpAicos/protocol"

	"github.com/eclipse/paho.golang/paho"
	"github.com/google/uuid"
)

var ch = make(chan map[string]string)

var mqttClient *paho.Client
var TIMEOUT = 10 * time.Second

// var received_message = ""
// var received_message_topic = ""

var InstanceInUse []int

//var mutex sync.Mutex // Mutex for protecting shared variable

type Payload struct {
	Operation string      `json:"operation"`
	Data      interface{} `json:"data,omitempty"`
}

func Connect(node_name string, broker_uri string, broker_port string) (bool, error) {
	var err error
	mqttClient, err = ConnectToMQTTBroker(node_name, broker_uri, broker_port)
	if err != nil {
		fmt.Println(err)
		return false, err
	}

	return true, err
}

func MessageHandler(m *paho.Publish) {
	fmt.Println("Received message: ", string(m.Payload))
	fmt.Println("In Topic: ", string(m.Topic))
	data := map[string]string{"topic": string(m.Topic), "msg": string(m.Payload)}
	fmt.Println("Received message: ", data["msg"])
	fmt.Println("In Topic: ", data["topic"])
	ch <- data
}

func ConnectToMQTTBroker(node_name string, broker_uri string, broker_port string) (*paho.Client, error) {
	// MQTT v5 support available here: https://github.com/eclipse/paho.golang
	// Followed the chat example: https://github.com/eclipse/paho.golang/blob/master/paho/cmd/chat/main.go
	/*server := flag.String("server", "127.0.0.1:1883", "The full URL of the MQTT server to connect to")
	//topic := flag.String("topic", hostname, "Topic to publish and receive the messages on")
	//qos := flag.Int("qos", 0, "The QoS to send the messages at")
	//name := flag.String("chatname", hostname, "The name to attach to your messages")
	clientid := flag.String("clientid", "virtual-kubelet"+nodeName, "A clientid for the connection")
	username := flag.String("username", "", "A username to authenticate to the MQTT server")
	password := flag.String("password", "", "Password to match username")
	flag.Parse()*/

	server := broker_uri + ":" + broker_port
	clientid := "virtual-kubelet-" + node_name
	username := ""
	password := ""
	retry := 0

	var conn net.Conn
	var err error
	for {
		conn, err = net.Dial("tcp", server)
		if err == nil {
			break
		}

		fmt.Println("Failed to connect to ", server, " : ", err)

		if retry < 10 {
			time.Sleep(5 * time.Second)
			retry++
		} else {
			fmt.Println("Maximum retries exceeded. Exiting...")
			return nil, err
		}
	}

	client := paho.NewClient(paho.ClientConfig{
		Router: paho.NewSingleHandlerRouter(MessageHandler),
		Conn:   conn,
	})

	cp := &paho.Connect{
		KeepAlive:  30,
		ClientID:   clientid,
		CleanStart: true,
		Username:   username,
		Password:   []byte(password),
	}

	if username != "" {
		cp.UsernameFlag = true
	}
	if password != "" {
		cp.PasswordFlag = true
	}

	ca, err := client.Connect(context.Background(), cp)
	if err != nil {
		fmt.Println(err)
	}
	if ca.ReasonCode != 0 {
		fmt.Printf("Failed to connect to %s : %d - %s\n", server, ca.ReasonCode, ca.Properties.ReasonString)
	}

	ic := make(chan os.Signal, 1)
	signal.Notify(ic, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ic
		if client != nil {
			d := &paho.Disconnect{ReasonCode: 0}
			err := client.Disconnect(d)
			if err != nil {
				fmt.Printf("failed to send Disconnect: %s\n", err)
			}
		}
		os.Exit(0)
	}()

	return client, err
}

func GetNodeAddress(far_edge_node_id string) (string, error) {
	fmt.Println("Get IP address from " + far_edge_node_id)

	id := uuid.New()
	response_topic := id.String()
	response_topic = strings.ReplaceAll(response_topic, "-", "")

	var err error
	var payloadBytes []byte
	if payloadBytes, err = json.Marshal(
		Payload{
			Operation: "GET",
		}); err != nil {
		fmt.Println(err)
		return "", err
	}

	pb := &paho.Publish{
		Topic:   far_edge_node_id + "/Connectivity_Monitoring/0",
		QoS:     1,
		Payload: payloadBytes,

		Properties: &paho.PublishProperties{
			ResponseTopic: response_topic,
			ContentType:   "application/json", // you might want to set this if the remote listener takes different formats
		},
	}

	uns := &paho.Unsubscribe{
		Topics: []string{
			response_topic,
		},
	}

	if _, err := mqttClient.Subscribe(context.Background(), &paho.Subscribe{
		Subscriptions: map[string]paho.SubscribeOptions{
			response_topic: {QoS: byte(1), NoLocal: true},
		},
	}); err != nil {
		fmt.Println(err)
		return "", err
	}

	if _, err = mqttClient.Publish(context.Background(), pb); err != nil {
		mqttClient.Unsubscribe(context.Background(), uns)
		fmt.Println(err)
		return "", err
	} else {
		var jsonMap map[string]interface{}
		// Use select to wait for data on the channel or timeout after one second
		startTime := time.Now()
		timeout := TIMEOUT
		done := false
		for !done {
			select {
			case data := <-ch:
				msg_topic := data["topic"]
				msg := data["msg"]
				endTime := time.Now()
				elapsedTime := endTime.Sub(startTime)
				timeout = TIMEOUT - elapsedTime
				if msg_topic == response_topic {
					json.Unmarshal([]byte(msg), &jsonMap)
					done = true
				}
			case <-time.After(timeout):
				mqttClient.Unsubscribe(context.Background(), uns)
				fmt.Println(errors.New("timeout ended without receiving data " + far_edge_node_id))
				return "", errors.New("timeout ended without receiving data")
			}
		}

		if response_code, ok := jsonMap["response_code"].(float64); ok {
			if response_code == 69 {
				// Check if the instance matches the package ID
				if instances, ok := jsonMap["sdfObject"].(map[string]interface{})["Connectivity_Monitoring"].([]interface{}); ok {
					for _, instance := range instances {
						//TODO: This will break if there are multiple addresses
						if addresses, ok := instance.(map[string]interface{})["sdfProperty"].(map[string]interface{})["IP_Addresses"]; ok {
							if address, ok := addresses.(string); ok {
								return address, nil
							}
						}
					}
				}
			}
		}
	}
	return "", fmt.Errorf("error getting node address data")
}

func CreateSwMngtInstance(far_edge_node_id string, instance_id string) error {
	fmt.Println("Creating software management with instance " + instance_id + " on node " + far_edge_node_id)
	//response_topic := "createinstance/" + instance_id + "/response"
	// Generate a new UUID
	id := uuid.New()
	response_topic := id.String()
	response_topic = strings.ReplaceAll(response_topic, "-", "")
	fmt.Println(response_topic)

	var err error
	var payloadBytes []byte
	if payloadBytes, err = json.Marshal(
		Payload{
			Operation: "POST",
			Data: map[string]interface{}{
				"label": instance_id,
			},
		}); err != nil {
		fmt.Println(err)
		return err
	}

	pb := &paho.Publish{
		Topic:   far_edge_node_id + "/LWM2M_Software_Management",
		QoS:     1, // whatever value you want (0,1,2)
		Payload: payloadBytes,

		Properties: &paho.PublishProperties{
			ResponseTopic: response_topic,
			ContentType:   "application/json", // you might want to set this if the remote listener takes different formats
		},
	}

	uns := &paho.Unsubscribe{
		Topics: []string{
			response_topic,
		},
	}

	if _, err := mqttClient.Subscribe(context.Background(), &paho.Subscribe{
		Subscriptions: map[string]paho.SubscribeOptions{
			response_topic: {QoS: byte(1), NoLocal: true},
		},
	}); err != nil {
		fmt.Println(err)
		return err
	}

	//var err error
	if _, err := mqttClient.Publish(context.Background(), pb); err != nil {
		mqttClient.Unsubscribe(context.Background(), uns)
		fmt.Println(err)
		return err
	} else {
		var jsonMap map[string]interface{}
		// Use select to wait for data on the channel or timeout after one second
		startTime := time.Now()
		timeout := TIMEOUT
		done := false
		for !done {
			select {
			case data := <-ch:
				msg_topic := data["topic"]
				msg := data["msg"]
				endTime := time.Now()
				elapsedTime := endTime.Sub(startTime)
				timeout = TIMEOUT - elapsedTime
				if msg_topic == response_topic {
					json.Unmarshal([]byte(msg), &jsonMap)
					// This is only needed if we wait the the new instance in the announce topic
					/*if _, ok := jsonMap[far_edge_node_id].(map[string]interface{}); ok {
						fmt.Println("Received message from expected sensor")
						done = true
					}*/
					done = true
				}
			case <-time.After(timeout):
				mqttClient.Unsubscribe(context.Background(), uns)
				fmt.Println(errors.New("Timeout ended without the creation of the software management instance " + instance_id))
				return errors.New("Timeout ended without the creation of the software management instance " + instance_id)
			}
		}
		mqttClient.Unsubscribe(context.Background(), uns)

		if response_code, ok := jsonMap["response_code"].(float64); ok {
			if response_code == 65 {
				return nil
			} else {
				fmt.Println("Error adding package to instance " + instance_id + " of client " + far_edge_node_id + ". Response code is: " + strconv.FormatFloat(response_code, 'f', 0, 64))
				return errors.New("Error adding package to instance " + instance_id + " of client " + far_edge_node_id + ". Response code is: " + strconv.FormatFloat(response_code, 'f', 0, 64))
			}
		} else {
			fmt.Println("Error adding package to instance " + instance_id + " of client " + far_edge_node_id + ". Invalid response message.")
			return errors.New("Error adding package to instance " + instance_id + " of client " + far_edge_node_id + ". Invalid response message.")
		}

		//In case we want to validate the new instance in the announce topic
		/*if thing, ok := jsonMap[far_edge_node_id].(map[string]interface{}); ok {
			if object, ok := thing["sdfObject"].(map[string]interface{}); ok {
				if sof_mngt, ok := object["LWM2M_Software_Management"].([]interface{}); ok {
					for _, item := range sof_mngt {
						if sof_mngt_item, ok := item.(map[string]interface{}); ok {
							if sof_mngt_item["label"] == instance_id {
								fmt.Println("Created software management id " + instance_id + " with success")
								return nil
							}
						}
					}
				} else {
					if sof_mngt, ok := object["LWM2M_Software_Management"].(map[string]interface{}); ok {
						if sof_mngt["label"] == instance_id {
							fmt.Println("Created software management id " + instance_id + " with success")
							return nil
						}
					}
				}
			}
		}
		fmt.Println(errors.New("Error creating software management instance " + instance_id))
		return errors.New("Error creating software management instance " + instance_id)*/
	}
}

func AddPackage(far_edge_node_id string, instance_id string, pckg string) error {
	fmt.Println("Add package " + pckg + " to software management instance " + instance_id + " on node " + far_edge_node_id)
	//response_topic := "add_package/" + far_edge_node_id + "/instance/" + instance_id
	// Generate a new UUID
	id := uuid.New()
	response_topic := id.String()
	response_topic = strings.ReplaceAll(response_topic, "-", "")
	fmt.Println(response_topic)

	data, err := os.ReadFile(pckg)
	if err == nil {
		//data_str := string(data)
		if len(data) != 0 {

			var parsedData interface{}
			if err := json.Unmarshal([]byte(data), &parsedData); err != nil {
				fmt.Println(err)
				return err
			}
			fmt.Println("Package is: ")
			fmt.Println(parsedData)
			var err error
			var payloadBytes []byte
			if payloadBytes, err = json.Marshal(
				Payload{
					Operation: "POST",
					Data:      parsedData,
				}); err != nil {
				fmt.Println(err)
				return err
			}
			fmt.Println("Payload is: ")
			fmt.Println(payloadBytes)
			/*data_str = strings.ReplaceAll(data_str, `"`, `\"`)
			data_str = `\"` + data_str + `\"`
			data_str = strings.ReplaceAll(data_str, "\n", "")
			data_str = strings.ReplaceAll(data_str, "\t", "")
			data_str = strings.ReplaceAll(data_str, " ", "")*/
			pb := &paho.Publish{
				Topic:   far_edge_node_id + "/LWM2M_Software_Management/" + instance_id + "/Property/Package",
				QoS:     1, // whatever value you want (0,1,2)
				Payload: payloadBytes,

				Properties: &paho.PublishProperties{
					ResponseTopic: response_topic,
					ContentType:   "application/json", // you might want to set this if the remote listener takes different formats
				},
			}

			uns := &paho.Unsubscribe{
				Topics: []string{
					response_topic,
				},
			}

			if _, err := mqttClient.Subscribe(context.Background(), &paho.Subscribe{
				Subscriptions: map[string]paho.SubscribeOptions{
					response_topic: {QoS: byte(1), NoLocal: true},
				},
			}); err != nil {
				fmt.Println("Error subscribing response topic: ", err)
				return err
			}

			if _, err = mqttClient.Publish(context.Background(), pb); err != nil {
				mqttClient.Unsubscribe(context.Background(), uns)
				fmt.Println("Error publishing request: ", err)
				return err
			} else {
				var jsonMap map[string]interface{}
				// Use select to wait for data on the channel or timeout after one second
				startTime := time.Now()
				timeout := TIMEOUT
				done := false
				for !done {
					select {
					case data := <-ch:
						msg_topic := data["topic"]
						msg := data["msg"]
						endTime := time.Now()
						elapsedTime := endTime.Sub(startTime)
						timeout = TIMEOUT - elapsedTime
						if msg_topic == response_topic {
							json.Unmarshal([]byte(msg), &jsonMap)
							done = true
						}
					case <-time.After(timeout):
						mqttClient.Unsubscribe(context.Background(), uns)
						fmt.Println(errors.New("Timeout ended without adding package to instance " + instance_id))
						return errors.New("Timeout ended without adding package to instance " + instance_id)
					}
				}
				mqttClient.Unsubscribe(context.Background(), uns)

				if response_code, ok := jsonMap["response_code"].(float64); ok {
					if response_code == 68 {
						return nil
					} else {
						fmt.Println("Error adding package to instance " + instance_id + " of client " + far_edge_node_id + ". Response code is: " + strconv.FormatFloat(response_code, 'f', 0, 64))
						return errors.New("Error adding package to instance " + instance_id + " of client " + far_edge_node_id + ". Response code is: " + strconv.FormatFloat(response_code, 'f', 0, 64))
					}
				} else {
					fmt.Println("Error adding package to instance " + instance_id + " of client " + far_edge_node_id + ". Invalid response message.")
					return errors.New("Error adding package to instance " + instance_id + " of client " + far_edge_node_id + ". Invalid response message.")
				}
			}
		} else {
			fmt.Println("Package with 0 contents.")
			return errors.New("package with 0 contents")
		}
	} else {
		fmt.Println("Error reading file: ", err)
		return err
	}
}

func InstallPackage(far_edge_node_id string, instance_id string) error {
	fmt.Println("Install package of software management instance " + instance_id + " of node " + far_edge_node_id)
	//response_topic := "install_package/" + far_edge_node_id + "/instance/" + instance_id
	// Generate a new UUID
	id := uuid.New()
	response_topic := id.String()
	response_topic = strings.ReplaceAll(response_topic, "-", "")
	fmt.Println(response_topic)

	var err error
	var payloadBytes []byte
	if payloadBytes, err = json.Marshal(
		Payload{
			Operation: "POST",
		}); err != nil {
		fmt.Println(err)
		return err
	}

	pb := &paho.Publish{
		Topic:   far_edge_node_id + "/LWM2M_Software_Management/" + instance_id + "/Action/Install",
		QoS:     1, // whatever value you want (0,1,2)
		Payload: payloadBytes,

		Properties: &paho.PublishProperties{
			ResponseTopic: response_topic,
			ContentType:   "application/json", // you might want to set this if the remote listener takes different formats
		},
	}

	uns := &paho.Unsubscribe{
		Topics: []string{
			response_topic,
		},
	}

	if _, err := mqttClient.Subscribe(context.Background(), &paho.Subscribe{
		Subscriptions: map[string]paho.SubscribeOptions{
			response_topic: {QoS: byte(1), NoLocal: true},
		},
	}); err != nil {
		fmt.Println(err)
		return err
	}

	if _, err := mqttClient.Publish(context.Background(), pb); err != nil {
		mqttClient.Unsubscribe(context.Background(), uns)
		fmt.Println(err)
		return err
	} else {
		var jsonMap map[string]interface{}
		// Use select to wait for data on the channel or timeout after one second
		startTime := time.Now()
		timeout := TIMEOUT
		done := false
		for !done {
			select {
			case data := <-ch:
				msg_topic := data["topic"]
				msg := data["msg"]
				endTime := time.Now()
				elapsedTime := endTime.Sub(startTime)
				timeout = TIMEOUT - elapsedTime
				if msg_topic == response_topic {
					json.Unmarshal([]byte(msg), &jsonMap)
					done = true
				}
			case <-time.After(timeout):
				mqttClient.Unsubscribe(context.Background(), uns)
				fmt.Println(errors.New("Timeout ended without installing package to instance " + instance_id))
				return errors.New("Timeout ended without installing package to instance " + instance_id)
			}
		}
		mqttClient.Unsubscribe(context.Background(), uns)
		if response_code, ok := jsonMap["response_code"].(float64); ok {
			if response_code == 68 {
				return nil
			} else {
				fmt.Println("Error installing package to instance " + instance_id + " of client " + far_edge_node_id + ". Response code is: " + strconv.FormatFloat(response_code, 'f', 0, 64))
				return errors.New("Error installing package to instance " + instance_id + " of client " + far_edge_node_id + ". Response code is: " + strconv.FormatFloat(response_code, 'f', 0, 64))
			}
		} else {
			fmt.Println("Error installing package to instance " + instance_id + " of client " + far_edge_node_id + ". Invalid response message.")
			return errors.New("Error installing package to instance " + instance_id + " of client " + far_edge_node_id + ". Invalid response message.")
		}
	}
}

func ActivatePackage(far_edge_node_id string, instance_id string) error {
	fmt.Println("Activate package of software management instance " + instance_id + " of node " + far_edge_node_id)
	//response_topic := "activate_package/" + far_edge_node_id + "/instance/" + instance_id
	id := uuid.New()
	response_topic := id.String()
	response_topic = strings.ReplaceAll(response_topic, "-", "")
	fmt.Println(response_topic)

	var err error
	var payloadBytes []byte
	if payloadBytes, err = json.Marshal(
		Payload{
			Operation: "POST",
		}); err != nil {
		fmt.Println(err)
		return err
	}

	pb := &paho.Publish{
		Topic:   far_edge_node_id + "/LWM2M_Software_Management/" + instance_id + "/Action/Activate",
		QoS:     1, // whatever value you want (0,1,2)
		Payload: payloadBytes,

		Properties: &paho.PublishProperties{
			ResponseTopic: response_topic,
			ContentType:   "application/json", // you might want to set this if the remote listener takes different formats
		},
	}

	uns := &paho.Unsubscribe{
		Topics: []string{
			response_topic,
		},
	}

	if _, err := mqttClient.Subscribe(context.Background(), &paho.Subscribe{
		Subscriptions: map[string]paho.SubscribeOptions{
			response_topic: {QoS: byte(1), NoLocal: true},
		},
	}); err != nil {
		fmt.Println(err)
		return err
	}

	if _, err = mqttClient.Publish(context.Background(), pb); err != nil {
		mqttClient.Unsubscribe(context.Background(), uns)
		fmt.Println(err)
		return err
	} else {
		var jsonMap map[string]interface{}
		// Use select to wait for data on the channel or timeout after one second
		startTime := time.Now()
		timeout := TIMEOUT
		done := false
		for !done {
			select {
			case data := <-ch:
				msg_topic := data["topic"]
				msg := data["msg"]
				endTime := time.Now()
				elapsedTime := endTime.Sub(startTime)
				timeout = TIMEOUT - elapsedTime
				if msg_topic == response_topic {
					json.Unmarshal([]byte(msg), &jsonMap)
					done = true
				}
			case <-time.After(timeout):
				mqttClient.Unsubscribe(context.Background(), uns)
				fmt.Println(errors.New("Timeout ended without activating package in instance " + instance_id))
				return errors.New("Timeout ended without activating package in instance " + instance_id)
			}
		}
		if response_code, ok := jsonMap["response_code"].(float64); ok {
			if response_code == 68 {
				return nil
			} else {
				fmt.Println("Error activating package on instance " + instance_id + " of client " + far_edge_node_id + ". Response code is: " + strconv.FormatFloat(response_code, 'f', 0, 64))
				return errors.New("Error activating package on instance " + instance_id + " of client " + far_edge_node_id + ". Response code is: " + strconv.FormatFloat(response_code, 'f', 0, 64))
			}
		} else {
			fmt.Println("Error activating package on instance " + instance_id + " of client " + far_edge_node_id + ". Invalid response message.")
			return errors.New("Error activating package on instance " + instance_id + " of client " + far_edge_node_id + ". Invalid response message.")
		}
	}
}

func DeployPackage(far_edge_node_id string, service_file string) (int, error) {
	//TODO: Need to arrange a better way to deal with the definition of instance id (one one hand the sensor might already come with an instance. On the other and if we reach the int number of deploys no more instances can be created)
	fmt.Println("Deploy package for  " + service_file + " in " + far_edge_node_id)
	instance_id := 0
	size := len(InstanceInUse)
	if size != 0 {
		instance_id = InstanceInUse[len(InstanceInUse)-1] + 1
	}
	err := CreateSwMngtInstance(far_edge_node_id, strconv.Itoa(instance_id))
	if err == nil {
		InstanceInUse = append(InstanceInUse, instance_id)
		err = AddPackage(far_edge_node_id, strconv.Itoa(instance_id), service_file)
		if err == nil {
			err = InstallPackage(far_edge_node_id, strconv.Itoa(instance_id))
			if err == nil {
				err = ActivatePackage(far_edge_node_id, strconv.Itoa(instance_id))
				if err != nil {
					//TODO: Delete the created software management instance
					RemovePackage(far_edge_node_id, instance_id)
					fmt.Println("Failed to activate Package")
				}
			} else {
				RemovePackage(far_edge_node_id, instance_id)
				fmt.Println("Failed to install Package")
			}
		} else {
			RemovePackage(far_edge_node_id, instance_id)
			fmt.Println("Failed to add package")
		}
	} else {
		fmt.Println("Failed to create Software Management Instance")
	}
	return instance_id, err
}

func RemovePackage(far_edge_node_id string, package_id int) error {
	fmt.Println("Remove package  " + strconv.Itoa(package_id) + " from " + far_edge_node_id)
	//response_topic := "remove_package/" + far_edge_node_id + "/instance/" + strconv.Itoa(package_id)
	id := uuid.New()
	response_topic := id.String()
	response_topic = strings.ReplaceAll(response_topic, "-", "")
	fmt.Println(response_topic)

	var err error
	var payloadBytes []byte
	if payloadBytes, err = json.Marshal(
		Payload{
			Operation: "DELETE",
		}); err != nil {
		fmt.Println(err)
		return err
	}

	pb := &paho.Publish{
		Topic:   far_edge_node_id + "/LWM2M_Software_Management/" + strconv.Itoa(package_id),
		QoS:     1, // whatever value you want (0,1,2)
		Payload: payloadBytes,

		Properties: &paho.PublishProperties{
			ResponseTopic: response_topic,
			ContentType:   "application/json", // you might want to set this if the remote listener takes different formats
		},
	}

	uns := &paho.Unsubscribe{
		Topics: []string{
			response_topic,
		},
	}

	if _, err := mqttClient.Subscribe(context.Background(), &paho.Subscribe{
		Subscriptions: map[string]paho.SubscribeOptions{
			response_topic: {QoS: byte(1), NoLocal: true},
		},
	}); err != nil {
		fmt.Println(err)
		return err
	}

	if _, err = mqttClient.Publish(context.Background(), pb); err != nil {
		mqttClient.Unsubscribe(context.Background(), uns)
		fmt.Println(err)
		return err
	} else {
		var jsonMap map[string]interface{}
		// Use select to wait for data on the channel or timeout after one second
		startTime := time.Now()
		timeout := TIMEOUT
		done := false
		for !done {
			select {
			case data := <-ch:
				msg_topic := data["topic"]
				msg := data["msg"]
				endTime := time.Now()
				elapsedTime := endTime.Sub(startTime)
				timeout = TIMEOUT - elapsedTime
				if msg_topic == response_topic {
					json.Unmarshal([]byte(msg), &jsonMap)
					done = true
				}
			case <-time.After(timeout):
				mqttClient.Unsubscribe(context.Background(), uns)
				fmt.Println(errors.New("Timeout ended without deleting package in instance " + strconv.Itoa(package_id)))
				return errors.New("Timeout ended without deleting package in instance " + strconv.Itoa(package_id))
			}
		}
		if response_code, ok := jsonMap["response_code"].(float64); ok {
			if response_code == 66 {
				index := sort.Search(len(InstanceInUse), func(i int) bool { return InstanceInUse[i] >= package_id })
				// Check if element exists
				if index < len(InstanceInUse) && InstanceInUse[index] == package_id {
					fmt.Println("Element", package_id, "is found in the slice.")
					InstanceInUse = append(InstanceInUse[:index], InstanceInUse[index+1:]...)
				} else {
					fmt.Println("Element", package_id, "is not found in InstanceInUse.")
				}
				return nil
			} else {
				fmt.Println("Error deleting package instance " + strconv.Itoa(package_id) + " of client " + far_edge_node_id + ". Response code is: " + strconv.FormatFloat(response_code, 'f', 0, 64))
				return errors.New("Error deleting package instance " + strconv.Itoa(package_id) + " of client " + far_edge_node_id + ". Response code is: " + strconv.FormatFloat(response_code, 'f', 0, 64))
			}
		} else {
			fmt.Println("Error deleting package instance " + strconv.Itoa(package_id) + " of client " + far_edge_node_id + ". Invalid response message.")
			return errors.New("Error deleting package instance " + strconv.Itoa(package_id) + " of client " + far_edge_node_id + ". Invalid response message.")
		}
	}
}

func PodRuntimeManagement(metrics string, namespace string, pod_name string) error {
	fmt.Println("PodRuntimeManagement for pod ", pod_name, " in namespace ", namespace)
	metric := strings.Split(metrics, ",")
	for i := 0; i < len(metric); i++ {
		fmt.Println("Metric ", i)
		fmt.Println(metric[i])
		metric_details := strings.Split(metric[i], " ")
		fmt.Println("metric_details ", metric_details)
		//TODO: Subscribe to MQTT topics in metric_details[0]
		//Use operator in metric_details[1] to compare with value in metric_details[2]
		//If operation is false call the operation bellow to delete the pod
		/*err = clientset.CoreV1().Pods(namespace).Delete(context.TODO(), podName, metav1.DeleteOptions{})
		if err != nil {
			panic(err.Error())
		}*/
	}
	return nil
}

func GetNodeStats(nodeId string) (protocol.ResourceStatistics, error) {
	fmt.Println("Get statistics from " + nodeId)
	stats := protocol.ResourceStatistics{}

	id := uuid.New()
	response_topic := id.String()
	response_topic = strings.ReplaceAll(response_topic, "-", "")

	var err error
	var payloadBytes []byte
	if payloadBytes, err = json.Marshal(
		Payload{
			Operation: "GET",
		}); err != nil {
		fmt.Println(err)
		return stats, err
	}

	pb := &paho.Publish{
		Topic:   nodeId + "/Software_Package_Monitoring",
		QoS:     1, // whatever value you want (0,1,2)
		Payload: payloadBytes,

		Properties: &paho.PublishProperties{
			ResponseTopic: response_topic,
			ContentType:   "application/json", // you might want to set this if the remote listener takes different formats
		},
	}

	uns := &paho.Unsubscribe{
		Topics: []string{
			response_topic,
		},
	}

	if _, err := mqttClient.Subscribe(context.Background(), &paho.Subscribe{
		Subscriptions: map[string]paho.SubscribeOptions{
			response_topic: {QoS: byte(1), NoLocal: true},
		},
	}); err != nil {
		fmt.Println(err)
		return stats, err
	}

	if _, err = mqttClient.Publish(context.Background(), pb); err != nil {
		mqttClient.Unsubscribe(context.Background(), uns)
		fmt.Println(err)
		return stats, err
	} else {
		var jsonMap map[string]interface{}
		// Use select to wait for data on the channel or timeout after one second
		startTime := time.Now()
		timeout := TIMEOUT
		done := false
		for !done {
			select {
			case data := <-ch:
				msg_topic := data["topic"]
				msg := data["msg"]
				endTime := time.Now()
				elapsedTime := endTime.Sub(startTime)
				timeout = TIMEOUT - elapsedTime
				if msg_topic == response_topic {
					json.Unmarshal([]byte(msg), &jsonMap)
					done = true
				}
			case <-time.After(timeout):
				mqttClient.Unsubscribe(context.Background(), uns)
				fmt.Println(errors.New("Timeout ended without receiving package statistics for node " + nodeId))
				return stats, errors.New("Timeout ended without receiving package statistics for node " + nodeId)
			}
		}

		if response_code, ok := jsonMap["response_code"].(float64); ok {
			if response_code == 69 {
				// Check if the instance matches the package ID
				if instances, ok := jsonMap["sdfObject"].(map[string]interface{})["Software_Package_Monitoring"].([]interface{}); ok {
					for _, instance := range instances {
						if softwarePackage, ok := instance.(map[string]interface{})["sdfProperty"].(map[string]interface{})["Sofware_Package"]; ok {
							if softwarePackage == "Device/0" {
								if cpuUsage, ok := instance.(map[string]interface{})["sdfProperty"].(map[string]interface{})["CPU_Usage"]; ok {
									stats.CpuUsage, _ = strconv.ParseFloat(cpuUsage.(string), 64)
									if memUsage, ok := instance.(map[string]interface{})["sdfProperty"].(map[string]interface{})["Memory_Usage"]; ok {
										stats.MemoryUsage, _ = strconv.ParseInt(memUsage.(string), 10, 64)
										if upTime, ok := instance.(map[string]interface{})["sdfProperty"].(map[string]interface{})["Uptime"]; ok {
											stats.Uptime, _ = strconv.ParseUint(upTime.(string), 10, 64)
											if packError, ok := instance.(map[string]interface{})["sdfProperty"].(map[string]interface{})["Error"]; ok {
												stats.Error, _ = strconv.ParseInt(packError.(string), 10, 32)
											} else {
												fmt.Println("Error parsing package statistics for node " + nodeId + " of client " + nodeId + ". Error on \"sdfProperty\" \"Error\"")
												return stats, errors.New("Error parsing package statistics for node " + nodeId + " of client " + nodeId + ". Error on \"sdfProperty\" \"Error\"")
											}
										} else {
											fmt.Println("Error parsing package statistics for node " + nodeId + " of client " + nodeId + ". Error on \"sdfProperty\" \"Uptime\"")
											return stats, errors.New("Error parsing package statistics for node " + nodeId + " of client " + nodeId + ". Error on \"sdfProperty\" \"Uptime\"")
										}
									} else {
										fmt.Println("Error parsing package statistics for node " + nodeId + " of client " + nodeId + ". Error on \"sdfProperty\" \"Memory_Usage\"")
										return stats, errors.New("Error parsing package statistics for node " + nodeId + " of client " + nodeId + ". Error on \"sdfProperty\" \"Memory_Usage\"")
									}
								} else {
									fmt.Println("Error parsing package statistics for node " + nodeId + " of client " + nodeId + ". Error on \"sdfObject\" \"CPU_Usage\"")
									return stats, errors.New("Error parsing package statistics for node " + nodeId + " of client " + nodeId + ". Error on \"sdfObject\" \"CPU_Usage\"")
								}
								return stats, nil
							}
						} else {
							fmt.Println("Error parsing package statistics for node " + nodeId + " of client " + nodeId + ". Error on \"sdfProperty\" \"Sofware_Package\"")
							return stats, errors.New("Error parsing package statistics for node " + nodeId + " of client " + nodeId + ". Error on \"sdfProperty\" \"Sofware_Package\"")
						}
					}
					return stats, errors.New("failed to find stats for requested package")
				} else {
					fmt.Println("Error parsing package statistics for node " + nodeId + " of client " + nodeId + ". Error on \"sdfObject\" \"Software_Package_Monitoring\"")
					return stats, errors.New("Error parsing package statistics for node " + nodeId + " of client " + nodeId + ". Error on \"sdfObject\" \"Software_Package_Monitoring\"")
				}
			} else {
				fmt.Println("Error reading package statistics for node " + nodeId + " of client " + nodeId + ". Response code is: " + strconv.FormatFloat(response_code, 'f', 0, 64))
				return stats, errors.New("Error reading package statistics for node " + nodeId + " of client " + nodeId + ". Response code is: " + strconv.FormatFloat(response_code, 'f', 0, 64))
			}
		} else {
			fmt.Println("Error reading package statistics for node " + nodeId + " of client " + nodeId + ". Invalid response message.")
			return stats, errors.New("Error reading package statistics for node " + nodeId + " of client " + nodeId + ". Invalid response message.")
		}
	}
}

func GetPackageStats(nodeId string, packageId int) (protocol.ResourceStatistics, error) {
	fmt.Println("Get package statistics " + strconv.Itoa(packageId) + " from " + nodeId)
	stats := protocol.ResourceStatistics{}

	id := uuid.New()
	response_topic := id.String()
	response_topic = strings.ReplaceAll(response_topic, "-", "")

	var err error
	var payloadBytes []byte
	if payloadBytes, err = json.Marshal(
		Payload{
			Operation: "GET",
		}); err != nil {
		fmt.Println(err)
		return stats, err
	}

	pb := &paho.Publish{
		Topic:   nodeId + "/Software_Package_Monitoring",
		QoS:     1, // whatever value you want (0,1,2)
		Payload: payloadBytes,

		Properties: &paho.PublishProperties{
			ResponseTopic: response_topic,
			ContentType:   "application/json", // you might want to set this if the remote listener takes different formats
		},
	}

	uns := &paho.Unsubscribe{
		Topics: []string{
			response_topic,
		},
	}

	if _, err := mqttClient.Subscribe(context.Background(), &paho.Subscribe{
		Subscriptions: map[string]paho.SubscribeOptions{
			response_topic: {QoS: byte(1), NoLocal: true},
		},
	}); err != nil {
		fmt.Println(err)
		return stats, err
	}

	if _, err = mqttClient.Publish(context.Background(), pb); err != nil {
		mqttClient.Unsubscribe(context.Background(), uns)
		fmt.Println(err)
		return stats, err
	} else {
		var jsonMap map[string]interface{}
		// Use select to wait for data on the channel or timeout after one second
		startTime := time.Now()
		timeout := TIMEOUT
		done := false
		for !done {
			select {
			case data := <-ch:
				msg_topic := data["topic"]
				msg := data["msg"]
				endTime := time.Now()
				elapsedTime := endTime.Sub(startTime)
				timeout = TIMEOUT - elapsedTime
				if msg_topic == response_topic {
					json.Unmarshal([]byte(msg), &jsonMap)
					done = true
				}
			case <-time.After(timeout):
				mqttClient.Unsubscribe(context.Background(), uns)
				fmt.Println(errors.New("Timeout ended without receiving package statistics for " + strconv.Itoa(packageId)))
				return stats, errors.New("Timeout ended without receiving package statistics for " + strconv.Itoa(packageId))
			}
		}

		if response_code, ok := jsonMap["response_code"].(float64); ok {
			if response_code == 69 {
				// Check if the instance matches the package ID
				if instances, ok := jsonMap["sdfObject"].(map[string]interface{})["Software_Package_Monitoring"].([]interface{}); ok {
					for _, instance := range instances {
						if softwarePackage, ok := instance.(map[string]interface{})["sdfProperty"].(map[string]interface{})["Sofware_Package"]; ok {
							if softwarePackage == "LWM2M Software Management/"+strconv.Itoa(packageId) {
								if cpuUsage, ok := instance.(map[string]interface{})["sdfProperty"].(map[string]interface{})["CPU_Usage"]; ok {
									stats.CpuUsage, _ = strconv.ParseFloat(cpuUsage.(string), 64)
									if memUsage, ok := instance.(map[string]interface{})["sdfProperty"].(map[string]interface{})["Memory_Usage"]; ok {
										stats.MemoryUsage, _ = strconv.ParseInt(memUsage.(string), 10, 64)
										if upTime, ok := instance.(map[string]interface{})["sdfProperty"].(map[string]interface{})["Uptime"]; ok {
											stats.Uptime, _ = strconv.ParseUint(upTime.(string), 10, 64)
											if packError, ok := instance.(map[string]interface{})["sdfProperty"].(map[string]interface{})["Error"]; ok {
												stats.Error, _ = strconv.ParseInt(packError.(string), 10, 32)
											} else {
												fmt.Println("Error parsing package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Error on \"sdfProperty\" \"Error\"")
												return stats, errors.New("Error parsing package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Error on \"sdfProperty\" \"Error\"")
											}
										} else {
											fmt.Println("Error parsing package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Error on \"sdfProperty\" \"Uptime\"")
											return stats, errors.New("Error parsing package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Error on \"sdfProperty\" \"Uptime\"")
										}
									} else {
										fmt.Println("Error parsing package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Error on \"sdfProperty\" \"Memory_Usage\"")
										return stats, errors.New("Error parsing package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Error on \"sdfProperty\" \"Memory_Usage\"")
									}
								} else {
									fmt.Println("Error parsing package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Error on \"sdfObject\" \"CPU_Usage\"")
									return stats, errors.New("Error parsing package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Error on \"sdfObject\" \"CPU_Usage\"")
								}
								return stats, nil
							}
						} else {
							fmt.Println("Error parsing package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Error on \"sdfProperty\" \"Sofware_Package\"")
							return stats, errors.New("Error parsing package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Error on \"sdfProperty\" \"Sofware_Package\"")
						}
					}
					return stats, errors.New("failed to find stats for requested package")
				} else {
					fmt.Println("Error parsing package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Error on \"sdfObject\" \"Software_Package_Monitoring\"")
					return stats, errors.New("Error parsing package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Error on \"sdfObject\" \"Software_Package_Monitoring\"")
				}
			} else {
				fmt.Println("Error reading package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Response code is: " + strconv.FormatFloat(response_code, 'f', 0, 64))
				return stats, errors.New("Error reading package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Response code is: " + strconv.FormatFloat(response_code, 'f', 0, 64))
			}
		} else {
			fmt.Println("Error reading package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Invalid response message.")
			return stats, errors.New("Error reading package statistics for " + strconv.Itoa(packageId) + " of client " + nodeId + ". Invalid response message.")
		}
	}
}

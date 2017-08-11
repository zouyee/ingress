package etcd

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"os"
	"strings"

	"github.com/coreos/etcd/client"
	"github.com/golang/glog"
	uuid "github.com/nu7hatch/gouuid"
	"golang.org/x/net/context"

	"github.com/zouyee/kube-cluster/core/types"
	"k8s.io/client-go/pkg/api/v1"
)

const (
	// BaseConfigPath ...
	//BaseConfigPath = "/Users/zoues/Code/golang/src/github.com/zouyee/kube-cluster/controller/cmd/controller"
	BaseConfigPath = "/etc/ansible"
	// BasicTaskPath ...
	BasicTaskPath = "/root/kargo/"
	// StepConfig ...
	StepConfig = "config"
	// StepPreflight  ...
	StepPreflight = "preflight"
	// StepPostflight ...
	StepPostflight = "postflight"

	// StatusProcessing ...
	StatusProcessing = "processing"
	// StatusDone ...
	StatusDone = "done"
	// StatusError ...
	StatusError = "error"
	// etcd auth path
	EtcdAuth = "/config/auth"
	// etcd image path
	EtcdImage = "/config/image"
	// Kargo ...
	Kargo = `
	{{- range $i, $node := .Cluster }}
	{{$node.Name}} ansible_ssh_host={{$node.Address}} {{if $node.Password}}ansible_ssh_pass={{$node.Password}}{{end}} ip={{$node.Address}}
	{{- end}}
	[kube-master]
	{{- range $i, $node := .Cluster }}
	{{if hasString $node.Role "master" }}{{$node.Name}}{{ end }}
	{{end -}}
	[etcd]
	{{- range $i, $node := .Cluster }}
	{{if hasString $node.Role "master" }}{{$node.Name}}{{ end }}
	{{end -}}
	[zookeeper]
	{{- range $i, $node := .Cluster }}
	{{if hasString $node.Role "zookeeper" }}{{$node.Name}}{{ end }}
	{{end -}}
	[kafka]
	{{- range $i, $node := .Cluster }}
	{{if hasString $node.Role "kafka" }}{{$node.Name}}{{ end }}
	{{end -}}
	[es]
	{{- range $i, $node := .Cluster }}
	{{if hasString $node.Role "es" }}{{$node.Name}}{{ end }}
	{{end -}}
	[logstash]
	{{- range $i, $node := .Cluster }}
	{{if hasString $node.Role "logstash" }}{{$node.Name}}{{ end }}
	{{end -}}
	[ingress]
	{{- range $i, $node := .Cluster }}
	{{if hasString $node.Role "ingress" }}{{$node.Name}}{{ end }}
	{{end -}}
	[dashboard]
	{{- range $i, $node := .Cluster }}
	{{if hasString $node.Role "dashboard" }}{{$node.Name}}{{ end }}
	{{end -}}
	[alertmanager]
	{{- range $i, $node := .Cluster }}
	{{if hasString $node.Role "alertmanager" }}{{$node.Name}}{{ end }}
	{{end -}}
	[resource-manager]
	{{- range $i, $node := .Cluster }}
	{{if hasString $node.Role "resource-manager" }}{{$node.Name}}{{ end }}
	{{end -}}
	[web-ssh]
	{{- range $i, $node := .Cluster }}
	{{if hasString $node.Role "web-ssh" }}{{$node.Name}}{{ end }}
	{{end -}}
	[kube-node]
	{{- range $i, $node := .Cluster }}
	{{if hasString $node.Role "slave" }}{{$node.Name}}{{ end }}
	{{end -}}
	[k8s-cluster:children]
	kube-node
	kube-master
	`
)

// ExistCluster ...
var ExistCluster = []string{"master", "slave", "web-ssh", "es", "resource-manager", "alertmanager"}

// Node ...
type Node struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion"`
	MetaData   Meta   `json:"metadata"`
	Spec       Status `json:"spec"`
	NodeInfo   Info   `json:"nodeInfo,omitempty"`
}

// Meta ...
type Meta struct {
	IP   string `json:"ip"`
	UID  string `json:"uid"`
	Port string `json:"port"`
}

// Status ...
type Status struct {
	HostName string   `json:"hostname"`
	Cluster  string   `json:"cluster,omitempty"`
	Health   string   `json:"health,omitempty"`
	Role     []string `json:"role,omitempty"`
}

// Info ...
type Info struct {
	UserName string `json:"username,omitempty"`
	PassWord string `json:"password,omitempty"`
	Ping     string `json:"ping,omitempty"`
}

// ClusterConfig Status Step will be config,preflight,deploy,postflight, status will be done,processing,error
type ClusterConfig struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Masters     []types.Endpoint `json:"masters"`
	Token       string           `json:"token"`
	ConfigPath  string           `json:"configpath"`
	Step        string           `json:"step"`
	Status      string           `json:"status"`
}

// IsExist ...
func IsExist(kapi client.KeysAPI, path string) bool {
	log.Printf("etcd is exist %s", path)
	_, err := kapi.Get(context.Background(), path, nil)
	if err != nil && client.IsKeyNotFound(err) {
		return false
	}
	return true
}

// SetClusterConfig ...
func SetClusterConfig(kapi client.KeysAPI, path string, config ClusterConfig) error {
	Config, err := json.Marshal(config)
	if err != nil {
		log.Printf("Set:json.Marshal not done. err is %q\n", err)
		return err
	}
	resp, err := kapi.Set(context.Background(), path, string(Config), nil)
	if err == nil {
		log.Printf("Set is done. Metadata is %q\n", resp)
	}
	return err
}

// GetClusterConfig ...
func GetClusterConfig(kapi client.KeysAPI, path string) (*ClusterConfig, error) {
	config := &ClusterConfig{}

	resp, err := kapi.Get(context.Background(), path, nil)
	if err == nil {
		log.Printf("Set is done. Metadata is %q\n", resp)
	}
	err = json.Unmarshal([]byte(resp.Node.Value), config)

	return config, err
}

// Get info
func Get(kapi client.KeysAPI, path string) (Node, error) {
	// get "/foo" key's value
	log.Printf("Getting %s key value", path)
	resp, err := kapi.Get(context.Background(), path, nil)

	if err != nil {
		return Node{}, err
	}
	// print common key info
	log.Printf("Get is done. Metadata is %q\n", resp)
	// print value
	model := &Node{}
	err = json.Unmarshal([]byte(resp.Node.Value), model)

	return *model, err
}

// Set info
func Set(kapi client.KeysAPI, path string, model *Node) error {
	Model, err := json.Marshal(model)
	if err != nil {
		log.Printf("Set:json.Marshal not done. err is %q\n", err)
		return err
	}

	resp, err := kapi.Set(context.Background(), path, string(Model), nil)
	if err == nil {

		log.Printf("Set is done. Metadata is %q\n", resp)
	}
	return err
}

// List info
func List(kapi client.KeysAPI, path string) ([]Node, error) {
	resp, err := kapi.Get(context.Background(), path, nil)
	if err != nil {
		log.Printf("list not done. err is %q\n", err)
	}
	nodes := make([]Node, len(resp.Node.Nodes))
	nodeinfo := Node{}
	for index, n := range resp.Node.Nodes {
		err = json.Unmarshal([]byte(n.Value), &nodeinfo)
		nodes[index] = nodeinfo
	}
	if err != nil {
		log.Fatal(err)
		return nil, err
	}
	return nodes, err

}

// ListCluster info
func ListCluster(kapi client.KeysAPI, path string) (*types.Backend, error) {
	log.Print("Getting '/cluster' key value")
	ctx := context.Background()
	resp, err := kapi.Get(ctx, "/cluster/", &client.GetOptions{
		Recursive: true,
		Sort:      true,
	})
	//nodeinfo := Node{}
	cluster := &types.Backend{}
	cluster.Cluster = make(map[string][]*types.Endpoint)
	if err != nil {
		glog.Info(err)

	} else {
		if !resp.Node.Dir {
			fmt.Println(resp.Node.Key)
		}
		for _, node := range resp.Node.Nodes {
			RPrint(ctx, node, cluster)
		}
		fmt.Printf("cluster info list %#v", cluster)
		for key, value := range cluster.Cluster {

			fmt.Printf("cluster key is %s, value is %s\n", key, value)
		}
	}

	auth, err := Get(kapi, "/config/auth")
	if err != nil {
		log.Fatal(err)
	}
	cluster.Auth = NodeToEndpoint(auth)

	image, err := Get(kapi, "/config/image")
	if err != nil {
		log.Fatal(err)
	}
	cluster.Image = NodeToEndpoint(image)
	return cluster, err

}

// Delete info
func Delete(kapi client.KeysAPI, path string) error {
	_, err := kapi.Delete(context.Background(), path, &client.DeleteOptions{Recursive: true})

	return err
}

// DeleteCluster ... node cluster
// Delete info
func DeleteCluster(kapi client.KeysAPI, path string, cluster string) error {
	_, err := kapi.Delete(context.Background(), path, &client.DeleteOptions{Recursive: true})
	resp, err := kapi.Get(context.Background(), "/node", nil)

	nodeinfo := &Node{}
	for _, n := range resp.Node.Nodes {
		err = json.Unmarshal([]byte(n.Value), nodeinfo)
		if nodeinfo.Spec.Cluster == cluster {
			nodeinfo.Spec.Cluster = ""
			Set(kapi, fmt.Sprintf("/node/%s", nodeinfo.Spec.HostName), nodeinfo)
		}
	}

	return err
}

// RPrint recursively prints out the nodes in the node structure.
func RPrint(c context.Context, n *client.Node, cluster *types.Backend) {
	if !n.Dir && !strings.Contains(n.Key, "config") {
		//fmt.Println(strings.Split(n.Key, "/")[2])
		key := strings.Split(n.Key, "/")[2]

		nodeinfo := Node{}
		endpoint := types.Endpoint{}
		err := json.Unmarshal([]byte(n.Value), &nodeinfo)
		// nodeinfo convert into endpoint
		endpoint.Cluster = key
		endpoint.Address = nodeinfo.MetaData.IP
		endpoint.Port = nodeinfo.MetaData.Port
		endpoint.Role = strings.Join(nodeinfo.Spec.Role, ",")
		endpoint.Name = nodeinfo.Spec.HostName
		// set cluster.Cluster
		cluster.Cluster[key] = append(cluster.Cluster[key], &endpoint)

		if err != nil {
			log.Fatalf("rprint unmarshall error is %#v", err)
		}
		//log.Printf("%q key has %q value\n,struct is %#v", n.Key, n.Value, nodeinfo)
	}

	for _, node := range n.Nodes {
		RPrint(c, node, cluster)
	}
}

// NodeToEndpoint ...
func NodeToEndpoint(node Node) types.Endpoint {
	ep := types.Endpoint{}
	ep.Name = node.Spec.HostName
	ep.Cluster = node.Spec.Cluster
	ep.Address = node.MetaData.IP
	ep.Port = node.MetaData.Port
	ep.Password = node.NodeInfo.PassWord
	ep.Role = strings.Join(node.Spec.Role, ",")
	return ep
}

// EndpointToNode ...
func EndpointToNode(ep types.Endpoint) Node {
	uid, err := uuid.NewV4()
	if err != nil {
		log.Fatal(err)
	}
	node := Node{
		APIVersion: "v1",
		Kind:       "Node",
		MetaData: Meta{
			IP:   ep.Address,
			UID:  fmt.Sprintf("%X", uid),
			Port: ep.Port,
		},
		Spec: Status{
			HostName: ep.Name,
			Cluster:  ep.Cluster,
			Role:     strings.Split(ep.Role, ","),
		},
		NodeInfo: Info{
			PassWord: ep.Password,
		},
	}

	return node
}

// KubeNodeToClusterInfo ...
func KubeNodeToClusterInfo(node v1.NodeList, name string) ClusterConfig {
	localNode := ClusterConfig{}
	localNode.Step = StepPostflight
	localNode.Status = StatusDone
	localNode.Name = name
	ep := types.Endpoint{}
	for _, item := range node.Items {
		if item.Labels["node-role.kubernetes.io/master"] == "true" {
			ep.Cluster = name
			ep.Name = item.Name
			ep.Address = item.Status.Addresses[0].Address
			ep.Role = ep.Role + ",master"
		}
		ep.Cluster = name
		ep.Name = item.Name
		ep.Address = item.Status.Addresses[0].Address
		ep.Role = ep.Role + ",slave"
	}
	return localNode
}

// GenerateHost ...
func GenerateHost(cluster *types.PostBackend) error {
	// 生成配置文件 toml
	tmpl, err := template.New("kargo").Funcs(template.FuncMap{
		"hasString": func(role string, feature string) bool {
			if strings.Contains(role, feature) {
				return true
			}
			return false
		},
	}).Parse(Kargo)
	if err != nil {
		log.Fatal("GenerateHost template error")
		return err
	}
	temp, err := os.OpenFile(fmt.Sprintf("%s/%s", BaseConfigPath, cluster.Name), os.O_CREATE|os.O_RDWR, 0660)
	if err != nil {
		log.Fatal("GenerateHost open template error")
		return err
	}
	err = tmpl.Execute(temp, cluster)
	if err != nil {
		log.Fatal("GenerateHost exec template error")
		return err
	}
	return err
}

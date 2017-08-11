package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"

	"github.com/coreos/etcd/client"
	uuid "github.com/nu7hatch/gouuid"
	"golang.org/x/net/context"
)

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

type Backend struct {
	Name    string                 `json:"name"`
	Cluster map[string][]*Endpoint `json:"cluster"`
	Monitor Endpoint               `json:"monitor"`
	Alert   Endpoint               `json:"alert"`
	Log     Endpoint               `json:"log"`
}

type Backends struct {
	Name    string                 `json:"name"`
	Cluster map[string][]*Endpoint `json:"cluster"`
	Monitor Endpoint               `json:"monitor"`
	Alert   Endpoint               `json:"alert"`
	Log     Endpoint               `json:"log"`
}

type Endpoint struct {
	Name     string `json:"name"`
	Cluster  string `json:"cluster"`
	Address  string `json:"address"`
	Port     string `json:"port"`
	Role     string `json:"role"`
	Password string `json:"password"`
}

// PostBackend ...
type PostBackend struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Cluster     []*Endpoint `json:"cluster"`
	Auth        Endpoint    `json:"auth"`
	Image       Endpoint    `json:"image"`
	Status      string      `json:"status"`
}

const kargo = `
{{- range $i, $node := .Cluster }}
{{$node.Name}} ansible_ssh_host={{$node.Address}} {{if $node.Password}}ansible_ssh_pass={{$node.Password}}{{end}} ip={{$node.Address}}
{{- end}}
[kube-master]
{{- range $i, $node := .Cluster }}
{{if hasString $node.Role "master" }}{{$node.Name}}{{ end -}}
{{end -}}
[etcd]
{{- range $i, $node := .Cluster }}
{{if hasString $node.Role "master" }}{{$node.Name}}{{ end -}}
{{end -}}
[kube-node]
{{- range $i, $node := .Cluster }}
{{if hasString $node.Role "master" }}{{$node.Name}}{{ end -}}
{{end -}}
[k8s-cluster:children]
kube-node
kube-master
`
const Kargo1 = `
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
[ingress]
{{- range $i, $node := .Cluster }}
{{if hasString $node.Role "ingress" }}{{$node.Name}}{{ end }}
{{end -}}
[web-ssh]
{{- range $i, $node := .Cluster }}
{{if hasString $node.Role "web-ssh" }}{{$node.Name}}{{ end }}
{{end -}}
[kube-node]
{{- range $i, $node := .Cluster }}
{{ if hasString $node.Role "slave" }}{{ $node.Name }}{{ end }}
{{end -}}
[k8s-cluster:children]
kube-node
kube-master
`

// Get info
func Get(kapi client.KeysAPI, path string, model *interface{}) (interface{}, error) {
	// get "/foo" key's value
	log.Printf("Getting %s key value", path)
	resp, err := kapi.Get(context.Background(), path, nil)

	if err != nil {
		return nil, err
	} else {
		// print common key info
		log.Printf("Get is done. Metadata is %q\n", resp)
		// print value
		err = json.Unmarshal([]byte(resp.Node.Value), model)
		log.Printf("%q key has %q value\n,struct is %#v", resp.Node.Key, resp.Node.Value, model)
	}
	return model, err
}

// Set info 不存在的path，通过set也是可以创建的
func Set(kapi client.KeysAPI, path string, model *interface{}) error {
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
func list(kapi client.KeysAPI, path string) []Node {
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
	}
	return nodes

}

// Delete info
func Delete(kapi client.KeysAPI, path string) error {
	_, err := kapi.Delete(context.Background(), path, &client.DeleteOptions{Recursive: true})
	return err
}

// rPrint recursively prints out the nodes in the node structure.
func rPrint(c context.Context, n *client.Node, cluster *Backend) {
	if !n.Dir && !strings.Contains(n.Key, "config") {
		//fmt.Println(strings.Split(n.Key, "/")[2])
		key := strings.Split(n.Key, "/")[2]

		nodeinfo := Node{}
		endpoint := Endpoint{}
		err := json.Unmarshal([]byte(n.Value), &nodeinfo)
		// nodeinfo convert into endpoint
		endpoint.Cluster = key
		endpoint.Address = nodeinfo.MetaData.IP
		endpoint.Port = nodeinfo.MetaData.Port
		endpoint.Role = strings.Join(nodeinfo.Spec.Role, ",")
		// set cluster.Cluster
		cluster.Cluster[key] = append(cluster.Cluster[key], &endpoint)

		if err != nil {
			log.Fatalf("rprint unmarshall error is %#v", err)
		}
		//log.Printf("%q key has %q value\n,struct is %#v", n.Key, n.Value, nodeinfo)
	}

	for _, node := range n.Nodes {
		rPrint(c, node, cluster)
	}
}

func testTempl() {
	/*
		cmd := exec.Command("/usr/bin/curl", "-X", "DELETE", fmt.Sprintf("http://%s:30499/api/v1/_raw/node/name/lk-k8s-minion-3", "10.132.47.92"))
		_, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Print(err)
		}
	*/
	tmpl, err := template.New("kargo").Funcs(template.FuncMap{
		"hasString": func(role string, feature string) bool {
			if strings.Contains(role, feature) {
				return true
			}
			return false
		},
	}).Parse(Kargo1)
	if err != nil {
		panic(err)
	}
	cluster := &PostBackend{}
	cluster.Name = "abc"

	master1 := Endpoint{
		Name:    "master-1",
		Cluster: "default",
		Address: "10.142.21.111",
		Port:    "22",
		Role:    "master,web-ssh",
	}
	master2 := Endpoint{
		Name:    "master-2",
		Cluster: "default",
		Address: "10.142.21.112",
		Port:    "22",
		Role:    "master,alertmanager",
	}
	master3 := Endpoint{
		Name:    "master-3",
		Cluster: "default",
		Address: "10.142.21.113",
		Port:    "22",
		Role:    "master",
	}
	slave1 := Endpoint{
		Name:    "slave-1",
		Cluster: "default",
		Address: "10.142.21.113",
		Port:    "22",
		Role:    "slave,ingress,dashboard",
	}
	slave2 := Endpoint{
		Name:    "slave-2",
		Cluster: "default",
		Address: "10.142.21.114",
		Port:    "22",
		Role:    "zookeeper,kafka,es,logstash,slave",
	}
	pwd, err := os.Getwd()
	if err != nil {
		fmt.Printf("getwd error is %v", err)
	}
	temp, err := os.OpenFile(fmt.Sprintf("%s/%s", pwd, cluster.Name), os.O_CREATE|os.O_RDWR, 0660)
	if err != nil {
		fmt.Printf("open %s error is %v", fmt.Sprintf("%s/%s", pwd, cluster.Name), err)
	}
	cluster.Cluster = append(cluster.Cluster, &master1, &master2, &master3, &slave1, &slave2)
	err = tmpl.Execute(temp, cluster)
	if err != nil {
		fmt.Printf("exec error is %v", err)
	}
	cmd := exec.Command("sed", "-i", "", "/^$/d", fmt.Sprintf("%s/%s", pwd, cluster.Name))
	_, err = cmd.CombinedOutput()
	if err != nil {
		fmt.Print(err)
	}

}

// NodeToEndpoint ...
func NodeToEndpoint(node Node) Endpoint {
	ep := Endpoint{}
	ep.Name = node.Spec.HostName
	ep.Cluster = node.Spec.Cluster
	ep.Address = node.MetaData.IP
	ep.Port = node.MetaData.Port
	ep.Password = node.NodeInfo.PassWord
	ep.Role = strings.Join(node.Spec.Role, ",")
	return ep
}
func main() {
	//testTempl()
	//os.Exit(1)
	cfg := client.Config{
		Endpoints: []string{"http://10.142.21.201:2379"},
		Transport: client.DefaultTransport,
		// set timeout per request to fail fast when the target endpoint is unavailable
		HeaderTimeoutPerRequest: 10 * time.Second,
	}
	c, err := client.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	kapi := client.NewKeysAPI(c)
	uid, err := uuid.NewV4()
	if err != nil {
		log.Fatal(err)
	}

	auth := &Node{
		Kind:       "Node",
		APIVersion: "v1",
		MetaData: Meta{
			IP:   "10.142.21.15",
			UID:  fmt.Sprintf("%X", uid),
			Port: "22",
		},
		Spec: Status{
			HostName: "host-1",
			Cluster:  "ww-test",
			Role:     []string{"master", "alertmanager", "es", "web-ssh", "resource-manager", "slave"},
		},
	}

	fmt.Printf("/cluster/%s/minions/%s \n", auth.Spec.Cluster, auth.Spec.HostName)
	Auth, err := json.Marshal(auth)
	if err != nil {
		log.Fatal(err)
	}
	resp, err := kapi.Set(context.Background(), fmt.Sprintf("/cluster/%s/minions/%s", auth.Spec.Cluster, auth.Spec.HostName), string(Auth), nil)
	if err != nil {
		log.Fatalf("err is %#v", err)
	} else {
		// print  key info
		log.Printf("Set is done. Metadata is %q\n", resp)
	}
	// get "/foo" key's value
	/*

		log.Print("Getting '/cluster' key value")
		ctx := context.Background()
		resp, err := kapi.Get(ctx, "/cluster/", &client.GetOptions{
			Recursive: true,
			Sort:      true,
		})
		//nodeinfo := Node{}
		if err != nil {
			log.Fatal(err)
		}
		cluster := &Backend{}
		cluster.Cluster = make(map[string][]*Endpoint)
		if !resp.Node.Dir {
			fmt.Println(resp.Node.Key)
		}
		for _, node := range resp.Node.Nodes {
			rPrint(ctx, node, cluster)
		}
		fmt.Printf("cluster info list %#v", cluster)
		for key, value := range cluster.Cluster {

			fmt.Printf("cluster key is %s, value is %s\n", key, value)
		}
		/*else {
			// print common key info
			log.Printf("Get is done. Metadata is %q\n", resp)
			// print value
			err = json.Unmarshal([]byte(resp.Node.Value), &nodeinfo)
			log.Printf("%q key has %q value\n,struct is %#v", resp.Node.Key, resp.Node.Value, nodeinfo)
		}
	*/

}

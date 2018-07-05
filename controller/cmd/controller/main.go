package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	go_reap "github.com/hashicorp/go-reap"
	"github.com/spf13/pflag"

	"github.com/zouyee/kube-cluster/core/etcd"
	"github.com/zouyee/kube-cluster/core/types"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 8192

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Time to wait before force close on connection.
	closeGracePeriod = 10 * time.Second

	// ansible path
	ansiblePath = "/usr/bin/ansible"
	// ansible hosts path
	ansibleInventory = "/root/kargo/inventory"
)

func pumpStdin(ws *websocket.Conn, w io.Writer) {
	defer ws.Close()
	ws.SetReadLimit(maxMessageSize)
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			break
		}
		message = append(message, '\n')
		if _, err := w.Write(message); err != nil {
			break
		}
	}
}

func pumpStdout(ws *websocket.Conn, r io.Reader, done chan struct{}) {
	defer func() {
	}()
	s := bufio.NewScanner(r)
	for s.Scan() {
		ws.SetWriteDeadline(time.Now().Add(writeWait))
		if err := ws.WriteMessage(websocket.TextMessage, s.Bytes()); err != nil {
			ws.Close()
			break
		}
	}
	if s.Err() != nil {
		log.Println("scan:", s.Err())
	}

	close(done)

	ws.SetWriteDeadline(time.Now().Add(writeWait))
	ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	time.Sleep(closeGracePeriod)
	ws.Close()
}

func ping(ws *websocket.Conn, done chan struct{}) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := ws.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(writeWait)); err != nil {
				log.Println("ping:", err)
			}
		case <-done:
			return
		}
	}
}

func internalError(ws *websocket.Conn, msg string, err error) {
	log.Println(msg, err)
	ws.WriteMessage(websocket.TextMessage, []byte("Internal server error."))
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func main() {
	go go_reap.ReapChildren(nil, nil, nil, nil)
	var (
		flags    = pflag.NewFlagSet("", pflag.ExitOnError)
		etcdHost = flags.String("etcd", "10.142.21.201:2379", "The address of the etcd server")
		rootPath = flags.String("root", "/opt/k8s/", "the root path of default server")
		port     = flags.Int("port", 8080, "backend port")
		auth     = flags.String("auth", "127.0.0.1:2341", "the address of auth")
		image    = flags.String("image", "127.0.0.1:8888", "the address of image server")
	)
	flags.AddGoFlagSet(flag.CommandLine)
	flags.Parse(os.Args)
	flag.Set("logtostderr", "true")
	// check out wether auth and image paramether would be provided
	if (len(*auth) == 0) && (len(*image) == 0) {
		glog.Exit("auth and image must be deploy before using kube-cluster")
	}
	// TODO checkout auth and image  component health

	// create nginx controller using provided paramether and exec Start func
	ngx := newNGINXController(*etcdHost, *rootPath, *auth, *image)

	// registry rest api
	go registerHandlers(*port, &ngx)

	// handle Interrupt signal
	go handleSigterm(&ngx,func(code int) {
		os.Exit(code)
	})

	// wait util SIGTERM signal
	glog.Infof("shut down cluster controller")
	for {
		glog.Infof("Handled quit, awaiting stopped")
		time.Sleep(30 * time.Second)
	}
}

func registerHandlers(port int, cc *NGINXController) {
	r := mux.NewRouter()
	s := r.PathPrefix("/api/v1").Subrouter()

	// 软件信息
	// Done
	s.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		b, _ := json.Marshal(cc.Info())
		w.Write(b)
	}).Methods("GET")

	// 停止进程
	// Done
	s.HandleFunc("/stop", func(w http.ResponseWriter, r *http.Request) {
		err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		if err != nil {
			glog.Errorf("unexcepted error: %v", err)
		}
	}).Methods("GET")

	// 获取节点列表:done
	// GET DATA: []etcd.Node
	// Done
	s.HandleFunc("/node", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Kube-Action", "node")
		nodes, err := etcd.List(cc.kapi, "/node")
		if err != nil {
			http.Error(w, "list node can not read data from body", http.StatusNoContent)
		}
		b, _ := json.Marshal(nodes)
		w.Write(b)
	}).Methods("GET")

	// 批量导入节点
	s.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Kube-Action", "node")
		data, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "add node can not read data from body", http.StatusNoContent)
		}
		nodes := []*etcd.Node{}
		err = json.Unmarshal(data, nodes)
		if err != nil {
			http.Error(w, "add node can not unmarshal data ", http.StatusNoContent)
		}
		for _, node := range nodes {
			ok := etcd.IsExist(cc.kapi, fmt.Sprintf("/node/%s", node.Spec.HostName))
			if ok {
				http.Error(w, "add node which is conflict", http.StatusConflict)
			}
			// 不存在，则设置node配置
			err = etcd.Set(cc.kapi, fmt.Sprintf("/node/%s", node.Spec.HostName), node)
			if err != nil {
				http.Error(w, "etcd can not add node ", http.StatusBadRequest)
			}
		}

	}).Methods("POST")
	// 增加节点(查看是否cluster为不为空，为空则只写etcd，否则将添加节点到cluster集群)
	// POST DATA: etcd.Node
	// TODO 需要增加校验集群与节点是否存在的接口
	// Basice Done
	s.HandleFunc("/node", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Kube-Action", "node")
		data, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "add node can not read data from body", http.StatusNoContent)
		}
		node := &etcd.Node{}
		err = json.Unmarshal(data, node)
		cluster := types.PostBackend{}
		cluster.Cluster = make([]*types.Endpoint, 0)
		if err != nil {
			http.Error(w, "add node can not unmarshal data ", http.StatusNoContent)
		}
		// 判断节点是否存在
		ok := etcd.IsExist(cc.kapi, fmt.Sprintf("/node/%s", node.Spec.HostName))
		if ok {
			http.Error(w, "add node which is conflict", http.StatusConflict)
		}
		// 不存在，则设置node配置
		err = etcd.Set(cc.kapi, fmt.Sprintf("/node/%s", node.Spec.HostName), node)
		// 判断集群是否存在
		ok = etcd.IsExist(cc.kapi, fmt.Sprintf("/cluster/%s/config", node.Spec.Cluster))

		if node.Spec.Cluster != "" && ok {
			// 集群存在，则设置集群节点配置
			err = etcd.Set(cc.kapi, fmt.Sprintf("/cluster/%s/minions/%s", node.Spec.HostName, node.Spec.Cluster), node)
			// 需要对接kargo：将节点加入到指定集群中---------------------------------------
			if err != nil {
				http.Error(w, "add node can not set in etcd", http.StatusBadRequest)
			}
			if strings.Contains(node.Spec.Role[0], "image") || strings.Contains(node.Spec.Role[0], "auth") {
				http.Error(w, "can not  add node which role is image or auth", http.StatusNoContent)
			}
			// NodeToEndpoint:1、转换node to endpoint
			ep := etcd.NodeToEndpoint(*node)
			cluster.Cluster = append(cluster.Cluster, &ep)
			// 生成kargo配置
			err = etcd.GenerateHost(&cluster)
			if err != nil {
				http.Error(w, "add node to current cluster which GenerateHost  kargo found error", http.StatusBadRequest)
			}
			// 判断kargo集群目录是否存在：make sure kargo config is exsit，如果存在则执行kargo加入，不然报错
			_, err = os.Stat(fmt.Sprintf("%s/%s", etcd.BaseConfigPath, node.Spec.Cluster))
			if (err == nil) && (ep.Role == "slave") {
				//
				cmd := exec.Command(ansiblePath, "-i", fmt.Sprintf("%s/%s/%s", etcd.BaseConfigPath, node.Spec.Cluster, "hosts"), "cluster.yml", "-t", "nodename")
				_, err = cmd.CombinedOutput()
				if err != nil {
					http.Error(w, "add node to current cluster which deployed with kargo found error", http.StatusBadRequest)
				}
			}
			if os.IsNotExist(err) {
				http.Error(w, "add node to current cluster which deployed without kargo or role is not slave", http.StatusBadRequest)
			}
			// exec cmd to add node
		} else {
			http.Error(w, "add node must set cluster value", http.StatusBadRequest)
		}

		if err != nil {
			http.Error(w, "add node could set value", http.StatusBadRequest)
		}
	}).Methods("POST")

	// 删除节点: done
	// Done
	s.HandleFunc("/node/{name}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		name := mux.Vars(r)["name"]
		// 判断节点是否存在
		// 存在：1、获取节点；2、删除节点
		// 删除节点包括：1、删除/node/{name} 2、集群/cluster/{name}/minions/{name} 3、执行k8s delete
		if etcd.IsExist(cc.kapi, "/node/"+name) {
			node, err := etcd.Get(cc.kapi, "/node/"+name)
			if err != nil {
				http.Error(w, "delete node can not unmarshal data", http.StatusBadRequest)
			}
			cluster := node.Spec.Cluster
			// 删除该节点
			err = etcd.Delete(cc.kapi, fmt.Sprintf("/node/%s", node.Spec.HostName))
			if err != nil {
				http.Error(w, "delete node can not delete from etcd ", http.StatusBadRequest)
			}

			// 判断节点集群是否存在
			// 存在则删除节点
			ok := etcd.IsExist(cc.kapi, fmt.Sprintf("/cluster/%s/config", cluster))
			// 查看是否属于集群
			if ok && len(node.Spec.Role) == 1 && node.Spec.Role[0] == "slave" {
				etcd.Delete(cc.kapi, fmt.Sprintf("/cluster/%s/minions/%s", node.Spec.Cluster, node.Spec.HostName))
				// kubelet: 真正删除该集群的node节点 kubelet delete node node.Name

				// kargo: 清楚节点的kubelet等
				cmd := exec.Command("/usr/bin/curl", "-X", "DELETE", fmt.Sprintf("http://%s:30499/api/v1/_raw/node/name/%s", node.MetaData.IP, node.Spec.HostName))
				_, err = cmd.CombinedOutput()
				if err != nil {
					http.Error(w, "delete node when exec delete err ", http.StatusBadRequest)
				}
			}
			if len(node.Spec.Role) > 1 {
				http.Error(w, "can not delete node which role not only slave ", http.StatusBadRequest)
			}
		} else {
			http.Error(w, "do not exist node", http.StatusBadRequest)
		}
	}).Methods("DELETE")

	// 更新节点: just update node info
	// 前端保证只修改info
	// Done
	s.HandleFunc("/node/{name}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)

		data, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "update node can not read data from body", http.StatusNoContent)
		}
		node := &etcd.Node{}
		err = json.Unmarshal(data, node)
		if err != nil {
			http.Error(w, "update node can not  Unmarshal data", http.StatusBadRequest)
		}
		// 1、判断节点存在 2、获取该节点
		// 判断节点是否存在
		ok := etcd.IsExist(cc.kapi, fmt.Sprintf("/node/%s", node.Spec.HostName))
		if !ok {
			http.Error(w, "update node which did not exist", http.StatusNotFound)
		}

		temp, err := etcd.Get(cc.kapi, fmt.Sprintf("/node/%s", node.Spec.HostName))
		if err != nil {
			http.Error(w, "update node can not get node from etcd", http.StatusNoContent)
		}
		// 更新该节点
		err = etcd.Set(cc.kapi, fmt.Sprintf("/node/%s", node.Spec.HostName), node)
		if err != nil {
			http.Error(w, "update node can not in etcd ", http.StatusBadRequest)
		}

		// 查看是否属于集群
		if temp.Spec.Cluster == node.Spec.Cluster {
			node.NodeInfo = temp.NodeInfo
			etcd.Set(cc.kapi, fmt.Sprintf("/cluster/%s/minions/%s", node.Spec.Cluster, node.Spec.HostName), node)
			// 该接口不操作kargo
		}
		/*
			// 更新模版
			content, err := cc.OnUpdate()
			if err != nil {
				glog.Errorf("Delete node, when OnUpdate err is happen: %v", err)
			}
			// Reload前会检查是否变化
			cc.Reload(content)
		*/
	}).Methods("PUT")

	// 创建集群分为四步：1、配置信息,更新节点信息 2、基础环境安装 3、集群安装 4、监测健康状态
	// 创建集群（一）:
	// 一、配置信息 /cluster/config:done
	// POST DATA: types.PostBackend
	/*
		post data:
		{
			"name": "default",
			"cluster": [
				{
					"name": "ys-2",
					"cluster": "default",
					"address": "10.142.21.152",
					"port": "30254",
					"role": "master",
					"password": "123456"
				},
				{
					"name": "ys-3",
					"cluster": "default",
					"address": "10.142.21.153",
					"port": "30254",
					"role": "master",
					"password": "123456"
				},
				{
					"name": "ys-5",
					"cluster": "default",
					"address": "10.142.21.155",
					"port": "30254",
					"role": "slave",
					"password": "123456"
				},
			],
			"auth": {
				"name": "ys-5",
				"cluster": "default",
				"address": "10.142.21.155",
				"port": "30254",
				"role": "auth",
				"password": "123456"
			},
			"image": {
				"name": "ys-5",
				"cluster": "default",
				"address": "10.142.21.155",
				"port": "30254",
				"role": "image",
				"password": "123456"
			}
		}

	*/
	// Done
	// TODO swrap http.Error
	s.HandleFunc("/cluster/config", func(w http.ResponseWriter, r *http.Request) {

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Kube-Action", "config")
		cluster := &types.PostBackend{}
		data, err := ioutil.ReadAll(r.Body)

		if err != nil {
			http.Error(w, "create cluster can not read data from body", http.StatusNoContent)
		}
		err = json.Unmarshal(data, cluster)
		if err != nil {
			http.Error(w, "create cluster can not unmarshal  data ", http.StatusBadRequest)
		}

		// add node info
		for _, ep := range cluster.Cluster {
			node := etcd.EndpointToNode(*ep)
			err = etcd.Set(cc.kapi, fmt.Sprintf("/node/%s", ep.Name), &node)
			if err != nil {
				http.Error(w, fmt.Sprintf("node %s set etcd value error in /node", ep.Name), http.StatusBadRequest)
			}
			err = etcd.Set(cc.kapi, fmt.Sprintf("/cluster/%s/minions/%s", ep.Cluster, ep.Name), &node)
			if err != nil {
				http.Error(w, fmt.Sprintf("node %s set etcd value error in /cluster", ep.Name), http.StatusBadRequest)
			}
		}
		//
		// 1、先判断该集群/cluster/{name}/config的值，将集群信息写入etcd,接口数据为Backend,并生成kargo执行配置文件，并写入/cluster/{name}/config 值
		// 2、完成配置操作后，更新／cluster/{name}/config状态
		if cluster.Status == "" || cluster.Status == "config" {
			exist := etcd.IsExist(cc.kapi, fmt.Sprintf("/cluster/%s/config", cluster.Name))
			if exist {
				http.Error(w, "cluster has exist", http.StatusBadRequest)
			}
		} else if cluster.Status == "reconfig" {
			log.Fatal("reconfig action will spent much time which do not  recommended")
			cmd := exec.Command("/usr/bin/ansible-playbook", "-i", fmt.Sprintf("%s/%s/hosts", etcd.BaseConfigPath, cluster), fmt.Sprintf("%s/%s", etcd.BasicTaskPath, "reset.yml"))
			_, err = cmd.CombinedOutput()
			if err != nil {
				http.Error(w, "delete cluster when exec delete cluster err ", http.StatusBadRequest)
			}
		}
		// add node info
		for _, ep := range cluster.Cluster {
			node := etcd.EndpointToNode(*ep)
			err = etcd.Set(cc.kapi, fmt.Sprintf("/node/%s", ep.Name), &node)
			if err != nil {
				http.Error(w, fmt.Sprintf("node %s set etcd value error in /node", ep.Name), http.StatusBadRequest)
			}
			err = etcd.Set(cc.kapi, fmt.Sprintf("/cluster/%s/minions/%s", ep.Cluster, ep.Name), &node)
			if err != nil {
				http.Error(w, fmt.Sprintf("node %s set etcd value error in /cluster", ep.Name), http.StatusBadRequest)
			}
		}

		// 将cluster写入ClusterConfig
		config := etcd.ClusterConfig{}
		config.Description = cluster.Description
		config.Name = cluster.Name
		config.Step = etcd.StepConfig
		config.Status = etcd.StatusProcessing
		config.ConfigPath = fmt.Sprintf("%s/%s", etcd.BaseConfigPath, cluster)

		for _, backend := range cluster.Cluster {
			if strings.Contains(backend.Role, "master") {

				config.Masters = append(config.Masters, *backend)
			}
		}

		err = etcd.SetClusterConfig(cc.kapi, fmt.Sprintf("/cluster/%s/config", cluster.Name), config)
		if err != nil {
			http.Error(w, fmt.Sprintf("cluster %s set etcd value error", cluster.Name), http.StatusBadRequest)
		}
		// 生成配置文件 toml
		tmpl, err := template.New("kargo").Funcs(template.FuncMap{
			"hasString": func(role string, feature string) bool {
				if strings.Contains(role, feature) {
					return true
				}
				return false
			},
		}).Parse(etcd.Kargo)
		if err != nil {
			http.Error(w, fmt.Sprintf("config cluster in template when parse file %s/%s happen error: %s", etcd.BaseConfigPath, cluster.Name, err), http.StatusBadGateway)
		}
		err = os.MkdirAll(fmt.Sprintf("%s/%s/", etcd.BaseConfigPath, cluster.Name), 0777)
		if err != nil {
			http.Error(w, fmt.Sprintf("config cluster in template when create dir %s/%s happen error: %s", etcd.BaseConfigPath, cluster.Name, err), http.StatusBadGateway)
		}
		temp, err := os.OpenFile(fmt.Sprintf("%s/%s/hosts", etcd.BaseConfigPath, cluster.Name), os.O_CREATE|os.O_RDWR, 0660)
		if err != nil {
			http.Error(w, fmt.Sprintf("config cluster in template when open file %s/%s happen error: %s", etcd.BaseConfigPath, cluster.Name, err), http.StatusBadGateway)
		}
		err = tmpl.Execute(temp, cluster)
		if err != nil {
			http.Error(w, fmt.Sprintf("cluster config exec, generate  %s config status happen error", cluster.Name), http.StatusBadRequest)
		}
		// sed file blank lines which only supported linux
		cmd := exec.Command("sed", "-i", "/^$/d", fmt.Sprintf("%s/%s/hosts", etcd.BaseConfigPath, cluster.Name))
		_, err = cmd.CombinedOutput()
		if err != nil {
			fmt.Print(err)
		}
		// update /cluster/config status
		config.Status = etcd.StatusDone
		err = etcd.SetClusterConfig(cc.kapi, fmt.Sprintf("/cluster/%s/config", cluster.Name), config)
		if err != nil {
			http.Error(w, fmt.Sprintf("cluster config done, update  %s config status happen error", cluster.Name), http.StatusBadRequest)
		}
	}).Methods("POST")

	// 创建集群(二)：
	// create cluster：deploy standard env: step /cluster/preflight
	// TODO 需要判断结果，并设置clusterconfig
	s.HandleFunc("/cluster/{name}/preflight", func(w http.ResponseWriter, r *http.Request) {

		// 2、kargo执行基础安装：首先判断/cluster/{name}/config的step是否为config,并且已经done，如果不是则返回step跟status
		// 3、执行kargo基础安装步骤，完成操作后，更新/cluster/{name}/config状态

		// First confirm the `step` value in  /cluster/{name}/config which would be setted `config` and the value of status is done，
		// if these values not in line with expectations, it will return step and status's values.
		// Execute kargo cmd to install the standard environment before deploy kubernetes cluster
		ws, err := upgrader.Upgrade(w, r, nil)

		if err != nil {
			log.Println("upgrade:", err)
			return
		}
		// 获取集群配置信息
		name := mux.Vars(r)["name"]
		config, err := etcd.GetClusterConfig(cc.kapi, fmt.Sprintf("/cluster/%s/config", name))
		if err != nil {
			http.Error(w, fmt.Sprintf("create cluster: get cluster  %s config happen error: %s", config.Name, err), http.StatusBadRequest)
		}
		if config.Step != etcd.StepConfig && config.Status != etcd.StatusDone {
			http.Error(w, fmt.Sprintf("create cluster:  cluster  is  %s, and status is %s, which do not satify excitation condition", config.Step, config.Status), http.StatusBadRequest)
		}

		defer ws.Close()

		outr, outw, err := os.Pipe()
		if err != nil {
			internalError(ws, "stdout:", err)
			return
		}
		defer outr.Close()
		defer outw.Close()

		inr, inw, err := os.Pipe()
		if err != nil {
			internalError(ws, "stdin:", err)
			return
		}
		defer inr.Close()
		defer inw.Close()
		var proc *os.Process

		proc, err = os.StartProcess(ansiblePath, []string{"/usr/bin/ansible-playbook", "-i", fmt.Sprintf("%s/%s/hosts", etcd.BaseConfigPath, config.Name), fmt.Sprintf("%s/%s", etcd.BasicTaskPath, "baseenv.yml")}, &os.ProcAttr{
			Files: []*os.File{inr, outw, outw},
		})

		if err != nil {
			internalError(ws, "start:", err)
			return
		}

		inr.Close()
		outw.Close()

		stdoutDone := make(chan struct{})
		go pumpStdout(ws, outr, stdoutDone)
		go ping(ws, stdoutDone)

		pumpStdin(ws, inw)

		// Some commands will exit when stdin is closed.
		inw.Close()

		// Other commands need a bonk on the head.
		if err := proc.Signal(os.Interrupt); err != nil {
			log.Println("inter:", err)
			config.Status = etcd.StatusError
		}

		select {
		case <-stdoutDone:
		case <-time.After(time.Second):
			// A bigger bonk on the head.
			if err := proc.Signal(os.Kill); err != nil {
				log.Println("term:", err)
			}
			<-stdoutDone
		}

		if _, err := proc.Wait(); err != nil {
			log.Println("wait:", err)
		}
	})

	// 创建集群(三)：
	// create cluster：deploy kubernetes cluster: step /cluster/postflight
	// TODO 需要判断结果，并设置clusterconfig
	s.HandleFunc("/cluster/{name}/postflight", func(w http.ResponseWriter, r *http.Request) {
		// post data 格式必须是types.Backend
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("upgrade:", err)
			return
		}

		name := mux.Vars(r)["name"]
		config, err := etcd.GetClusterConfig(cc.kapi, fmt.Sprintf("/cluster/%s/config", name))
		if err != nil {
			http.Error(w, fmt.Sprintf("create cluster: get cluster  %s config happen error: %s", config.Name, err), http.StatusBadRequest)
		}
		if config.Step != etcd.StepPreflight && config.Status != etcd.StatusDone {
			http.Error(w, fmt.Sprintf("create cluster:  cluster  is  %s, and status is %s, which do not satify excitation condition", config.Step, config.Status), http.StatusBadRequest)
		}

		// 2、kargo执行后期安装：首先判断/cluster/{name}/config的step是否为preflight,并且已经done，如果不是则返回step跟status
		// 3、执行kargo后期安装步骤，完成操作后，更新/cluster/{name}/config状态
		// 4、如果step=postflight, status=done,那么update且reload nginx.conf

		defer ws.Close()

		outr, outw, err := os.Pipe()
		if err != nil {
			internalError(ws, "stdout:", err)
			return
		}
		defer outr.Close()
		defer outw.Close()

		inr, inw, err := os.Pipe()
		if err != nil {
			internalError(ws, "stdin:", err)
			return
		}
		defer inr.Close()
		defer inw.Close()
		var proc *os.Process

		proc, err = os.StartProcess(ansiblePath, []string{"/usr/bin/ansible-playbook", "-i", fmt.Sprintf("%s/%s/hosts", etcd.BaseConfigPath, config.Name), fmt.Sprintf("%s/%s", etcd.BasicTaskPath, "cluster.yml")}, &os.ProcAttr{
			Files: []*os.File{inr, outw, outw},
		})

		if err != nil {
			internalError(ws, "start:", err)
			return
		}

		inr.Close()
		outw.Close()

		stdoutDone := make(chan struct{})
		go pumpStdout(ws, outr, stdoutDone)
		go ping(ws, stdoutDone)

		pumpStdin(ws, inw)

		// Some commands will exit when stdin is closed.
		inw.Close()

		// Other commands need a bonk on the head.
		if err := proc.Signal(os.Interrupt); err != nil {
			log.Println("inter:", err)
			config.Status = etcd.StatusError
		}

		select {
		case <-stdoutDone:
		case <-time.After(time.Second):
			// A bigger bonk on the head.
			if err := proc.Signal(os.Kill); err != nil {
				log.Println("term:", err)
			}
			<-stdoutDone
		}

		if _, err := proc.Wait(); err != nil {
			log.Println("wait:", err)
		}

	})

	// reload nginx
	// when create cluster in postflight or delete cluster in success, we need revoke this rest api
	s.HandleFunc("/cluster/reload", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)

		content, err := cc.OnUpdate()
		if err != nil {
			http.Error(w, fmt.Sprintf("update cluster config in nginx happen %v", err), http.StatusBadGateway)
		}
		// Reload前会检查是否变化
		cc.Reload(content)

	}).Methods("POST")

	// Get cluster list
	// Done
	s.HandleFunc("/cluster", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Kube-Action", "cluster")

		cluster, err := etcd.ListCluster(cc.kapi, "/cluster")
		if err != nil {
			http.Error(w, "get cluster can not read data from body", http.StatusNoContent)
		}

		if err := json.NewEncoder(w).Encode(cluster); err != nil {
			glog.Errorf("Failed to encode responses to json: %s", err)
		}

	}).Methods("GET")

	// add exist cluster
	// Data: PostBackend
	// Done
	s.HandleFunc("/cluster", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)

		cluster := &types.PostBackend{}
		data, err := ioutil.ReadAll(r.Body)

		if err != nil {
			http.Error(w, "create cluster can not read data from body", http.StatusNoContent)
		}
		err = json.Unmarshal(data, cluster)
		if err != nil {
			http.Error(w, "create cluster can not unmarshal  data ", http.StatusBadRequest)
		}

		// add node info
		for _, ep := range cluster.Cluster {
			node := etcd.EndpointToNode(*ep)
			err = etcd.Set(cc.kapi, fmt.Sprintf("/node/%s", ep.Name), &node)
			if err != nil {
				http.Error(w, fmt.Sprintf("node %s set etcd value error in /node", ep.Name), http.StatusBadRequest)
			}
			err = etcd.Set(cc.kapi, fmt.Sprintf("/cluster/%s/minions/%s", ep.Cluster, ep.Name), &node)
			if err != nil {
				http.Error(w, fmt.Sprintf("node %s set etcd value error in /cluster", ep.Name), http.StatusBadRequest)
			}
		}

		config := etcd.ClusterConfig{}
		config.Description = cluster.Description
		config.Name = cluster.Name
		config.Step = etcd.StepPostflight
		config.Status = etcd.StatusDone
		config.ConfigPath = fmt.Sprintf("%s/%s", etcd.BaseConfigPath, cluster)

		for _, backend := range cluster.Cluster {
			if strings.Contains(backend.Role, "master") {

				config.Masters = append(config.Masters, *backend)
			}
		}

		err = etcd.SetClusterConfig(cc.kapi, fmt.Sprintf("/cluster/%s/config", cluster.Name), config)
		if err != nil {
			http.Error(w, fmt.Sprintf("cluster %s set etcd value error", cluster.Name), http.StatusBadRequest)
		}

	}).Methods("POST")

	// Delete cluster
	// 删除集群，且更新节点
	// TODO 实现单一reload接口
	s.HandleFunc("/cluster/{name}", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)

		cluster := mux.Vars(req)["name"]
		req.ParseForm()
		complete := strings.ToLower(req.FormValue("complete"))

		err := etcd.DeleteCluster(cc.kapi, fmt.Sprintf("/cluster/%s", cluster), cluster)
		// 需要执行kargo删除集群
		if err != nil {
			http.Error(w, "delete cluster can not read data from etcd", http.StatusBadRequest)
		}
		if complete == "true" {
			_, err := os.Stat(fmt.Sprintf("%s/%s/hosts", etcd.BaseConfigPath, cluster))
			if err != nil {
				http.Error(w, fmt.Sprintf("config cluster in template when open file %s/%s happen error: %s", etcd.BaseConfigPath, cluster, err), http.StatusBadGateway)
			}
			// kargo: 清楚节点的kubelet等
			cmd := exec.Command("/usr/bin/ansible-playbook", "-i", fmt.Sprintf("%s/%s", etcd.BaseConfigPath, cluster), fmt.Sprintf("%s/%s", etcd.BasicTaskPath, "reset.yml"))
			_, err = cmd.CombinedOutput()
			if err != nil {
				http.Error(w, "delete cluster when exec delete cluster err ", http.StatusBadRequest)
			}
		}
		// TODO update node status
	}).Methods("DELETE")

	// input  exsit cluster data for  sync kube-cluster
	// Done
	s.HandleFunc("/cluster/{name}/info", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		data, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "delete cluster can not read data from body", http.StatusNoContent)
		}
		clusterInfo := &etcd.ClusterConfig{}
		err = json.Unmarshal(data, clusterInfo)
		if err != nil {
			http.Error(w, "add node can not unmarshal data ", http.StatusNoContent)
		}
		err = etcd.SetClusterConfig(cc.kapi, fmt.Sprintf("/cluster/%s/config", clusterInfo.Name), *clusterInfo)
		if err != nil {
			http.Error(w, fmt.Sprintf("cluster %s set etcd value error", clusterInfo.Name), http.StatusBadRequest)
		}
	}).Methods("POST")

	// GET cluster{name} info
	// Done
	s.HandleFunc("/cluster/{name}/info", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		name := mux.Vars(r)["name"]
		clusterinfo, err := etcd.GetClusterConfig(cc.kapi, fmt.Sprintf("/cluster/%s/config", name))
		if err != nil {
			http.Error(w, fmt.Sprintf("cluster %s set etcd value error", name), http.StatusBadRequest)
		}
		b, _ := json.Marshal(clusterinfo)
		w.Write(b)
	}).Methods("GET")

	server := &http.Server{
		Addr:    fmt.Sprintf(":%v", port),
		Handler: r,
	}

	glog.Fatal(server.ListenAndServe())
}

type exiter func(code int)

func handleSigterm(cc *NGINXController, exit exiter) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM)
	<-signalChan
	glog.Infof("Received SIGTERM, shutting down")

	exitCode := 0
	if err := cc.Stop(); err != nil {
		glog.Infof("Error during shutdown: %v", err)
		exitCode = 1
	}

	glog.Infof("Handled quit, awaiting Pod deletion")
	time.Sleep(10 * time.Second)

	glog.Infof("Exiting with %v", exitCode)
	exit(exitCode)
}

package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
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
	ansibleInventory = "/etc/ansible/kargo/inventory/inventory"
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

var upgrader = websocket.Upgrader{}

func serveWs(w http.ResponseWriter, r *http.Request) {
	action := w.Header().Get("Kube-Action")
	method := r.Method

	data, err := ioutil.ReadAll(r.Body)
	if err != nil {
		glog.Errorf("serveWs can not read data from body:%s", err)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade:", err)
		return
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
	switch {
	case action == "cluster" && method == "POST": // 创建集群
		// 处理配置文件生成
		cluster := &types.Backend{}
		err = json.Unmarshal(data, cluster)
		if err != nil {
			internalError(ws, "stdin:", err)
			return
		}
		proc, err = os.StartProcess(ansiblePath, []string{ansiblePath, "create"}, &os.ProcAttr{
			Files: []*os.File{inr, outw, outw},
		})

	case action == "cluster" && method == "DELETE": // 删除集群
		// 处理配置文件生成
		cluster := &types.Backend{}
		err = json.Unmarshal(data, cluster)
		if err != nil {
			internalError(ws, "stdin:", err)
			return
		}
		proc, err = os.StartProcess(ansiblePath, []string{ansiblePath, "delete"}, &os.ProcAttr{
			Files: []*os.File{inr, outw, outw},
		})
	case action == "node" && method == "POST": // 增加节点
		// 处理配置文件生成
		node := &etcd.Node{}
		err = json.Unmarshal(data, node)
		if err != nil {
			internalError(ws, "stdin:", err)
			return
		}
		proc, err = os.StartProcess(ansiblePath, []string{ansiblePath, "create"}, &os.ProcAttr{
			Files: []*os.File{inr, outw, outw},
		})
	case action == "node" && method == "DELETE": // 删除节点
		// 处理配置文件生成
		node := &etcd.Node{}
		err = json.Unmarshal(data, node)
		if err != nil {
			internalError(ws, "stdin:", err)
			return
		}
		proc, err = os.StartProcess(ansiblePath, []string{ansiblePath, "delete"}, &os.ProcAttr{
			Files: []*os.File{inr, outw, outw},
		})
	}

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
}

func main() {
	go go_reap.ReapChildren(nil, nil, nil, nil)
	var (
		flags    = pflag.NewFlagSet("", pflag.ExitOnError)
		etcdHost = flags.String("etcd", "127.0.0.1:2379", "The address of the etcd server")
		rootPath = flags.String("root", "/opt/k8s/", "the root path of default server")
		port     = flags.Int("port", 8080, "backend port")
		auth     = flags.String("auth", "127.0.0.1:2341", "the address of auth")
		image    = flags.String("image", "127.0.0.1:8888", "the address of image server")
	)
	flags.AddGoFlagSet(flag.CommandLine)
	flags.Parse(os.Args)
	flag.Set("logtostderr", "true")

	ngx := newNGINXController(*etcdHost, *rootPath, *auth, *image)

	ngx.Start()
	go registerHandlers(*port, &ngx)
	go handleSigterm(&ngx)

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

	s.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		b, _ := json.Marshal(cc.Info())
		w.Write(b)
	}).Methods("GET")

	s.HandleFunc("/stop", func(w http.ResponseWriter, r *http.Request) {
		err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		if err != nil {
			glog.Errorf("unexcepted error: %v", err)
		}
	}).Methods("GET")

	// 获取节点列表
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

	// 增加节点
	s.HandleFunc("/node", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Kube-Action", "node")
		data, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "add node can not read data from body", http.StatusNoContent)
		}
		node := &etcd.Node{}
		err = json.Unmarshal(data, node)
		if err != nil {
			http.Error(w, "add node can not unmarshal data ", http.StatusNoContent)
		}
		_, err = etcd.Get(cc.kapi, fmt.Sprintf("/node/%s", node.Spec.HostName))
		if err == nil {
			http.Error(w, "add node which is conflict", http.StatusConflict)
		}
		err = etcd.Set(cc.kapi, fmt.Sprintf("/node/%s", node.Spec.HostName), node)
		// 有集群属性则加入集群
		if node.Spec.Cluster != "" {
			err = etcd.Set(cc.kapi, fmt.Sprintf("/cluster/%s/minions/%s", node.Spec.HostName, node.Spec.Cluster), node)
		}
		// 需要对接kargo：将节点加入到指定集群中

		if err != nil {
			http.Error(w, "add node could set value", http.StatusBadRequest)
		}

		// 更新模版
		content, err := cc.OnUpdate()
		if err != nil {
			glog.Errorf("add node, when OnUpdate err is happen: %v", err)
		}
		// Reload前会检查是否变化
		cc.Reload(content)

	}).Methods("POST")

	// 删除节点
	s.HandleFunc("/node", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Kube-Action", "node")
		data, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "delete node can not read data from body", http.StatusNoContent)
		}
		node := &types.Endpoint{}
		err = json.Unmarshal(data, node)
		if err != nil {
			http.Error(w, "delete node can not unmarshal data", http.StatusBadRequest)
		}
		// 删除该节点
		err = etcd.Delete(cc.kapi, fmt.Sprintf("/node/%s", node.Name))
		if err != nil {
			http.Error(w, "delete node can not delete from etcd ", http.StatusBadRequest)
		}
		// 查看是否属于集群
		if len(node.Cluster) > 0 {
			etcd.Delete(cc.kapi, fmt.Sprintf("/cluster/%s/minions/%s", node.Cluster, node.Name))
		}
		// 更新模版
		content, err := cc.OnUpdate()
		if err != nil {
			glog.Errorf("Delete node, when OnUpdate err is happen: %v", err)
		}
		// Reload前会检查是否变化
		cc.Reload(content)

	}).Methods("DELETE")

	// 更新节点
	s.HandleFunc("/node", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Kube-Action", "node")
		data, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "update node can not read data from body", http.StatusNoContent)
		}
		node := &etcd.Node{}
		err = json.Unmarshal(data, node)
		if err != nil {
			http.Error(w, "update node can not  Unmarshal data", http.StatusBadRequest)
		}
		// 获取该节点
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
		if temp.Spec.Cluster != node.Spec.Cluster {
			etcd.Set(cc.kapi, fmt.Sprintf("/cluster/%s/minions/%s", node.Spec.Cluster, node.Spec.HostName), node)
			// kargo 将节点添加到指定集群
		}
		// 更新模版
		content, err := cc.OnUpdate()
		if err != nil {
			glog.Errorf("Delete node, when OnUpdate err is happen: %v", err)
		}
		// Reload前会检查是否变化
		cc.Reload(content)

	}).Methods("DELETE")

	s.HandleFunc("/cluster", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Kube-Action", "cluster")
		cluster := &types.Backend{}
		data, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "create cluster can not read data from body", http.StatusNoContent)
		}
		err = json.Unmarshal(data, cluster)
		if err != nil {
			http.Error(w, "create cluster can not unmarshal  data ", http.StatusBadRequest)
		}
	}).Methods("POST")

	s.HandleFunc("/cluster", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Kube-Action", "cluster")
		cluster := &types.Backend{}
		data, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "get cluster can not read data from body", http.StatusNoContent)
		}
		err = json.Unmarshal(data, cluster)
		if err != nil {
			http.Error(w, "get cluster can not unmarshal data ", http.StatusBadRequest)
		}
	}).Methods("GET")

	s.HandleFunc("/cluster", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Kube-Action", "cluster")
		data, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "delete cluster can not read data from body", http.StatusNoContent)
		}
		cluster := string(data)
		err = etcd.Delete(cc.kapi, fmt.Sprintf("/cluster/%s", cluster))
		// 需要执行kargo删除集群
		if err != nil {
			http.Error(w, "delete cluster can not read data from etcd", http.StatusBadRequest)
		}
	}).Methods("DELETE")

	server := &http.Server{
		Addr:    fmt.Sprintf(":%v", port),
		Handler: r,
	}

	glog.Fatal(server.ListenAndServe())
}

func handleSigterm(cc *NGINXController) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM)
	<-signalChan
	glog.Info("Received SIGTERM,shutting down")
	os.Exit(0)
}

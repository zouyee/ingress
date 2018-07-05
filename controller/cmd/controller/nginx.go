package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/coreos/etcd/client"
	"github.com/golang/glog"
	uuid "github.com/nu7hatch/gouuid"
	ps "github.com/mitchellh/go-ps"
	"github.com/zouyee/kube-cluster/controller/config"
	ngx_template "github.com/zouyee/kube-cluster/controller/template"
	"github.com/zouyee/kube-cluster/controller/version"
	"github.com/zouyee/kube-cluster/core/etcd"
	"github.com/zouyee/kube-cluster/core/types"
)

type statusModule string

const (
	ngxHealthPort = 18080
	ngxHealthPath = "/healthz"

	errNoChild = "wait: no child processes"
	//tmplPath   = "/usr/local/openresty/nginx/template/nginx.tmpl"
	tmplPath = "/Users/zoues/Code/golang/src/github.com/zouyee/kube-cluster/controller/cmd/controller/nginx.tmpl"
	cfgPath  = "/Users/zoues/Code/golang/src/github.com/zouyee/kube-cluster/controller/cmd/controller/nginx.conf"
	//cfgPath  = "/usr/local/openresty/nginx/conf/nginx.conf"
	binary   = "/usr/local/openresty/nginx/sbin/nginx"
	//testPath = "/usr/local/openresty/nginx/conf/nginx-cfg"
	testPath  = "/Users/zoues/Code/golang/src/github.com/zouyee/kube-cluster/controller/cmd/controller/nginx-conf"
	
)

// newNGINXController creates a new NGINX cluster controller.
// If the environment variable NGINX_BINARY exists it will be used
// as source for nginx commands
func newNGINXController(etcdHost, rootPath, auth, image string) NGINXController {
	ngx := os.Getenv("NGINX_BINARY")
	if ngx == "" {
		ngx = binary
	}
	// create etcd cliet config
	cfg := client.Config{
		Endpoints: []string{fmt.Sprintf("http://%s", etcdHost)},
		Transport: client.DefaultTransport,
		// set timeout per request to fail fast when the target endpoint is unavailable
		HeaderTimeoutPerRequest: time.Second,
	}

	// using etcd config to create new KeysAPI which using etcd v2 version
	c, err := client.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	kapi := client.NewKeysAPI(c)

	// create uid using uuid
	uid, err := uuid.NewV4()
	if err != nil {
		log.Fatal(err)
	}
	// Set AuthNode info
	authNode := &etcd.Node{
		Kind:       "Node",
		APIVersion: "v1",
		MetaData: etcd.Meta{
			IP:   strings.Split(auth, ":")[0],
			UID:  fmt.Sprintf("%X", uid),
			Port: strings.Split(auth, ":")[1],
		},
		Spec: etcd.Status{
			HostName: strings.Split(auth, ":")[0],
			Role:     []string{"auth"},
		},
	}
	log.Print("Setting '/config/auth' key with auth value")
	if !etcd.IsExist(kapi, etcd.EtcdAuth) {
		err = etcd.Set(kapi, "/config/auth", authNode)
		if err != nil {
			fmt.Printf("newNGINXController Set Auth err: %#v", err)
		}
		err = etcd.Set(kapi, fmt.Sprintf("/node/%s", authNode.Spec.HostName), authNode)
		if err != nil {
			fmt.Printf("newNGINXController Set Auth err: %#v", err)
		}
	}

	// Set Image info
	uid, err = uuid.NewV4()
	if err != nil {
		log.Fatal(err)
	}
	imageNode := &etcd.Node{
		Kind:       "Node",
		APIVersion: "v1",
		MetaData: etcd.Meta{
			IP:   strings.Split(image, ":")[0],
			UID:  fmt.Sprintf("%X", uid),
			Port: strings.Split(image, ":")[1],
		},
		Spec: etcd.Status{
			HostName: strings.Split(image, ":")[0],
			Role:     []string{"image"},
		},
	}
	log.Print("Setting '/config/image' key with auth value")
	//log.Fatal(err)
	if !etcd.IsExist(kapi, etcd.EtcdImage) {
		err = etcd.Set(kapi, "/config/image", imageNode)
		if err != nil {
			fmt.Printf("newNGINXController Set Auth err: %#v", err)
		}
		err = etcd.Set(kapi, fmt.Sprintf("/node/%s", imageNode.Spec.HostName), imageNode)
		if err != nil {
			fmt.Printf("newNGINXController Set Auth err: %#v", err)
		}
	}
	n := &NGINXController{
		binary: ngx,
		kapi:   kapi,
		auth:   auth,
		image:  image,
	}

	var onChange func()
	onChange = func() {
		template, err := ngx_template.NewTemplate(tmplPath, onChange)
		if err != nil {
			// this error is different from the rest because it must be clear why nginx is not working
			glog.Errorf(`
-------------------------------------------------------------------------------
Error loading new template : %v
-------------------------------------------------------------------------------
`, err)
			return
		}

		n.t.Close()
		n.t = template
		glog.Info("new NGINX template loaded")
	}

	ngxTpl, err := ngx_template.NewTemplate(tmplPath, onChange)
	if err != nil {
		glog.Fatalf("invalid NGINX template: %v", err)
	}

	n.t = ngxTpl

	go n.Start()

	return *n
}

// NGINXController ...
type NGINXController struct {
	binary string
	t      *ngx_template.Template

	kapi client.KeysAPI

	auth string

	image string
}

// Start start a new NGINX master process running in foreground.
func (n *NGINXController) Start() {
	glog.Info("starting NGINX process...")
	content, err := n.OnUpdate()
	if err != nil {
		glog.Error("update cluster config in nginx happen")
	}
	// Reload前会检查是否变化
	err = ioutil.WriteFile(cfgPath, content, 0644)
	if err != nil {
		glog.Error("write cluster config in nginx happen")
	}
	done := make(chan error, 1)
	cmd := exec.Command(n.binary, "-c", cfgPath)
	n.start(cmd, done)

	// if the nginx master process dies the workers continue to process requests,
	// passing checks but in case of updates in ingress no updates will be
	// reflected in the nginx configuration which can lead to confusion and report
	// issues because of this behavior.
	// To avoid this issue we restart nginx in case of errors.
	for {
		err := <-done
		if exitError, ok := err.(*exec.ExitError); ok {
			waitStatus := exitError.Sys().(syscall.WaitStatus)
			glog.Warningf(`
-------------------------------------------------------------------------------
NGINX master process died (%v): %v
-------------------------------------------------------------------------------
`, waitStatus.ExitStatus(), err)
		}
		cmd.Process.Release()
		cmd = exec.Command(n.binary, "-c", cfgPath)
		// we wait until the workers are killed
		for {
			conn, err := net.DialTimeout("tcp", "127.0.0.1:80", 10*time.Second)
			if err != nil {
				break
			}
			conn.Close()
			time.Sleep(10 * time.Second)
		}
		// start a new nginx master process
		n.start(cmd, done)
	}
}

func (n *NGINXController) start(cmd *exec.Cmd, done chan error) {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		glog.Fatalf("nginx error: %v", err)
		done <- err
		return
	}

	go func() {
		done <- cmd.Wait()
	}()
}

// OnUpdate update template
func (n *NGINXController) OnUpdate() ([]byte, error) {

	cfg := config.NewDefault()
	cfg.Auth.Address = strings.Split(n.auth, ":")[0]
	cfg.Auth.Port = strings.Split(n.auth, ":")[1]

	cfg.Image.Address = strings.Split(n.image, ":")[0]
	cfg.Image.Port = strings.Split(n.image, ":")[1]

	// the limit of open files is per worker process
	// and we leave some room to avoid consuming all the FDs available
	wp, err := strconv.Atoi(cfg.WorkerProcesses)
	if err != nil {
		wp = 1
	}

	maxOpenFiles := (sysctlFSFileMax() / wp) - 1024
	if maxOpenFiles < 0 {
		// this means the value of RLIMIT_NOFILE is too low.
		maxOpenFiles = 1024
	}
	// need fix
	cluster := &types.Backend{}
	cluster.Cluster = make(map[string][]*types.Endpoint)
	// 需要添加backend-各个集群的节点

	/*
		使用etcd查询接口，获取所有集群，然后获取该集群的所有节点
	*/

	log.Print("Getting '/cluster' key value")
	ctx := context.Background()

	resp, err := n.kapi.Get(ctx, "/cluster/", &client.GetOptions{
		Recursive: true,
		Sort:      true,
	})
	//nodeinfo := Node{}
	if err != nil {
		glog.Info("The current do not has cluster")
	} else {
		for _, node := range resp.Node.Nodes {
			etcd.RPrint(ctx, node, cluster)
		}
	}

	auth, err := etcd.Get(n.kapi, "/config/auth")
	if err != nil {
		log.Fatal(err)
	}
	Auth := etcd.NodeToEndpoint(auth)
	image, err := etcd.Get(n.kapi, "/config/image")
	if err != nil {
		log.Fatal(err)
	}
	Image := etcd.NodeToEndpoint(image)
	cluster.Auth = Auth
	cluster.Image = Image

	content, err := n.t.Write(config.TemplateConfig{
		MaxOpenFiles: maxOpenFiles,
		Backends:     cluster,
		Cfg:          cfg,
	})
	if err != nil {
		glog.Errorf("write data found error %v", err)
		return nil, err
	}

	if err := n.testTemplate(content); err != nil {
		return nil, err
	}

	return content, nil
}

// Reload checks if the running configuration file is different
// to the specified and reload nginx if required
func (n NGINXController) Reload(data []byte) ([]byte, bool, error) {
	if !n.isReloadRequired(data) {
		return []byte("Reload not required"), false, nil
	}

	err := ioutil.WriteFile(cfgPath, data, 0644)
	if err != nil {
		return nil, false, err
	}

	o, e := exec.Command(n.binary, "-s", "reload").CombinedOutput()

	return o, true, e
}

// isReloadRequired check if the new configuration file is different
// from the current one.
func (n NGINXController) isReloadRequired(data []byte) bool {
	in, err := os.Open(cfgPath)
	if err != nil {
		return false
	}
	src, err := ioutil.ReadAll(in)
	in.Close()
	if err != nil {
		return false
	}

	if !bytes.Equal(src, data) {

		tmpfile, err := ioutil.TempFile("", "nginx-cfg-diff")
		if err != nil {
			glog.Errorf("error creating temporal file: %s", err)
			return false
		}
		defer tmpfile.Close()
		err = ioutil.WriteFile(tmpfile.Name(), data, 0644)
		if err != nil {
			return false
		}

		diffOutput, err := diff(src, data)
		if err != nil {
			glog.Errorf("error computing diff: %s", err)
			return true
		}

		if glog.V(2) {
			glog.Infof("NGINX configuration diff\n")
			glog.Infof("%v", string(diffOutput))
		}
		os.Remove(tmpfile.Name())
		return len(diffOutput) > 0
	}
	return false
}

// Info return build information
func (n NGINXController) Info() *types.BackendInfo {
	return &types.BackendInfo{
		Name:    "NGINX",
		Release: version.RELEASE,
		Build:   version.COMMIT,
	}
}

// testTemplate checks if the NGINX configuration inside the byte array is valid
// running the command "nginx -t" using a temporal file.
func (n NGINXController) testTemplate(cfg []byte) error {
	if len(cfg) == 0 {
		glog.Error("cfg lenth is zero")
		return fmt.Errorf("invalid nginx configuration (empty)")
	}

	err := ioutil.WriteFile(testPath, cfg, 0644)
	if err != nil {
		return err
	}
	out, err := exec.Command(n.binary, "-t", "-c", testPath).CombinedOutput()
	if err != nil && err.Error() != errNoChild {
		// this error is different from the rest because it must be clear why nginx is not working
		oe := fmt.Sprintf(`
-------------------------------------------------------------------------------
Error: %v
%v
-------------------------------------------------------------------------------
`, err, string(out))
		return errors.New(oe)
	}

	return nil
}

// Name returns the healthcheck name
func (n NGINXController) Name() string {
	return "Cluster Controller"
}

// Check returns if the nginx healthz endpoint is returning ok (status code 200)
func (n NGINXController) Check(_ *http.Request) error {
	res, err := http.Get(fmt.Sprintf("http://localhost:%v%v", ngxHealthPort, ngxHealthPath))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("ingress controller is not healthy")
	}
	return nil
}

func nginxExecCommand(args ...string) *exec.Cmd {
	ngx := os.Getenv("NGINX_BINARY")
	if ngx == "" {
		ngx = binary
	}

	cmdArgs := []string{"-c", cfgPath}
	cmdArgs = append(cmdArgs, args...)
	return exec.Command(ngx, cmdArgs...)
}

// Stop gracefully stops the NGINX master process.
func (n *NGINXController) Stop() error {
	// send stop signal to NGINX
	glog.Info("Stopping NGINX process")
	cmd := nginxExecCommand("-s", "quit")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		return err
	}

	// wait for the NGINX process to terminate
	timer := time.NewTicker(time.Second * 1)
	for range timer.C {
		if !IsNginxRunning() {
			glog.Info("NGINX process has stopped")
			timer.Stop()
			break
		}
	}

	return nil
}

// IsNginxRunning returns true if a process with the name 'nginx' is found
func IsNginxRunning() bool {
	processes, _ := ps.Processes()
	for _, p := range processes {
		if p.Executable() == "nginx" {
			return true
		}
	}
	return false
}
package main

import (
	"bytes"
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

	vtsStatusModule statusModule = "vts"

	errNoChild = "wait: no child processes"
)

var (
	tmplPath        = "/usr/local/openresty/nginx/template/nginx.tmpl"
	cfgPath         = "/usr/local/openresty/nginx/conf/nginx.conf"
	binary          = "/usr/local/openresty/nginx/sbin/nginx"
	defIngressClass = "nginx"
)

// newNGINXController creates a new NGINX cluster controller.
// If the environment variable NGINX_BINARY exists it will be used
// as source for nginx commands
func newNGINXController(etcdHost, rootPath, auth, image string) NGINXController {
	ngx := os.Getenv("NGINX_BINARY")
	if ngx == "" {
		ngx = binary
	}
	cfg := client.Config{
		Endpoints: []string{fmt.Sprintf("http://%s:2379", etcdHost)},
		Transport: client.DefaultTransport,
		// set timeout per request to fail fast when the target endpoint is unavailable
		HeaderTimeoutPerRequest: time.Second,
	}
	c, err := client.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	kapi := client.NewKeysAPI(c)

	// create uid
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
		},
	}
	log.Print("Setting '/config/auth' key with auth value")

	err = etcd.Set(kapi, "/config/auth", authNode)
	if err != nil {
		fmt.Printf("newNGINXController Set Auth err: %#v", err)
	}

	// Set Image info
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
		},
	}
	log.Print("Setting '/config/image' key with auth value")
	log.Fatal(err)

	err = etcd.Set(kapi, "/config/image", imageNode)
	if err != nil {
		fmt.Printf("newNGINXController Set Auth err: %#v", err)
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
			conn, err := net.DialTimeout("tcp", "127.0.0.1:80", 1*time.Second)
			if err != nil {
				break
			}
			conn.Close()
			time.Sleep(1 * time.Second)
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
	content, err := n.t.Write(config.TemplateConfig{
		MaxOpenFiles: maxOpenFiles,
		Backends:     cluster,
		Cfg:          cfg,
	})
	if err != nil {
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
		return fmt.Errorf("invalid nginx configuration (empty)")
	}
	tmpfile, err := ioutil.TempFile("", "nginx-cfg")
	if err != nil {
		return err
	}
	defer tmpfile.Close()
	err = ioutil.WriteFile(tmpfile.Name(), cfg, 0644)
	if err != nil {
		return err
	}
	out, err := exec.Command(n.binary, "-t", "-c", tmpfile.Name()).CombinedOutput()
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

	os.Remove(tmpfile.Name())
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

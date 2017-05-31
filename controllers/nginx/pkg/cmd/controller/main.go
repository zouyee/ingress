/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang/glog"
	go_reap "github.com/hashicorp/go-reap"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/pflag"

	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/ingress/core/pkg/ingress"
	"k8s.io/ingress/core/pkg/ingress/controller"
)

func main() {
	go go_reap.ReapChildren(nil, nil, nil, nil)
	var (
		flags    = pglag.NewFlagSet("", pflag.ExitOnError)
		etcdHost = flags.String("etcd-host", "127.0.0.1:2379", "The address of the etcd server")
		rootPath = flags.String("root-path", "/opt/k8s/", "the root path of default server")
	)
	flags.AddGoFlagSet(flag.CommandLine)
	flags.Parse(os.Args)
	flag.Set("logtostderr", "true")

	// start a new nginx controller
	ngx := newNGINXController(etcdHost, rootPath)
	// create a custom Ingress controller using NGINX as backend
	go registerHandlers(*profiling, *healthzPort, ic)
	go handleSigterm(ic)
	// start the controller
	ngx.Start()
	// wait
	glog.Infof("shutting down Ingress controller...")
	for {
		glog.Infof("Handled quit, awaiting pod deletion")
		time.Sleep(30 * time.Second)
	}
}

func registerHandlers(enableProfiling bool, port int, ic ingress.Controller) {
	mux := http.NewServeMux()
	// expose health check endpoint (/healthz)
	healthz.InstallHandler(mux,
		healthz.PingHealthz,
		ic.cfg.Backend,
	)

	mux.Handle("/metrics", promhttp.Handler())

	mux.HandleFunc("/build", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		b, _ := json.Marshal(ic.Info())
		w.Write(b)
	})

	mux.HandleFunc("/stop", func(w http.ResponseWriter, r *http.Request) {
		err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		if err != nil {
			glog.Errorf("unexpected error: %v", err)
		}
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%v", port),
		Handler: mux,
	}
	glog.Fatal(server.ListenAndServe())
}

func handleSigterm(ic *controller.GenericController) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM)
	<-signalChan
	glog.Infof("Received SIGTERM, shutting down")

	exitCode := 0
	if err := ic.Stop(); err != nil {
		glog.Infof("Error during shutdown %v", err)
		exitCode = 1
	}

	glog.Infof("Exiting with %v", exitCode)
	os.Exit(exitCode)
}

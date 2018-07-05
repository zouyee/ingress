package template

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	text_template "text/template"

	"github.com/zouyee/kube-cluster/core/watch"

	"github.com/golang/glog"
	"github.com/mitchellh/mapstructure"

	"github.com/zouyee/kube-cluster/controller/config"
	"github.com/zouyee/kube-cluster/core/types"
)

const (
	slash         = "/"
	defBufferSize = 65535
	errNoChild    = "wait: no child processes"
	cleanScript   = "/src/github.com/zouyee/kube-cluster/controller/rootfs/cluster-controller/clean-nginx-conf.sh"
)

// Template ...
type Template struct {
	tmpl      *text_template.Template
	fw        watch.FileWatcher
	s         int
	tmplBuf   *bytes.Buffer
	outCmdBuf *bytes.Buffer
}

// ReadConfig ...
func ReadConfig(src map[string]string) types.Configuration {
	conf := map[string]string{}
	if src != nil {
		// we need to copy the configmap data because the content is altered
		for k, v := range src {
			conf[k] = v
		}
	}

	to := config.NewDefault()

	config := &mapstructure.DecoderConfig{
		Metadata:         nil,
		WeaklyTypedInput: true,
		Result:           &to,
		TagName:          "json",
	}

	decoder, err := mapstructure.NewDecoder(config)
	if err != nil {
		glog.Warningf("unexpected error merging defaults: %v", err)
	}
	err = decoder.Decode(conf)
	if err != nil {
		glog.Warningf("unexpected error merging defaults: %v", err)
	}

	return to
}

//NewTemplate returns a new Template instance or an
//error if the specified template file contains errors
func NewTemplate(file string, onChange func()) (*Template, error) {
	tmpl, err := text_template.New("nginx.tmpl").Funcs(funcMap).ParseFiles(file)
	if err != nil {
		return nil, err
	}
	fw, err := watch.NewFileWatcher(file, onChange)
	if err != nil {
		return nil, err
	}

	return &Template{
		tmpl:      tmpl,
		fw:        fw,
		s:         defBufferSize,
		tmplBuf:   bytes.NewBuffer(make([]byte, 0, defBufferSize)),
		outCmdBuf: bytes.NewBuffer(make([]byte, 0, defBufferSize)),
	}, nil
}

// Close removes the file watcher
func (t *Template) Close() {
	t.fw.Close()
}

// Write populates a buffer using a template with NGINX configuration
// and the servers and upstreams created by Ingress rules
func (t *Template) Write(conf config.TemplateConfig) ([]byte, error) {
	defer t.tmplBuf.Reset()
	defer t.outCmdBuf.Reset()

	defer func() {
		if t.s < t.tmplBuf.Cap() {
			glog.V(2).Infof("adjusting template buffer size from %v to %v", t.s, t.tmplBuf.Cap())
			t.s = t.tmplBuf.Cap()
			t.tmplBuf = bytes.NewBuffer(make([]byte, 0, t.tmplBuf.Cap()))
			t.outCmdBuf = bytes.NewBuffer(make([]byte, 0, t.outCmdBuf.Cap()))
		}
	}()

	if glog.V(3) {
		b, err := json.Marshal(conf)
		if err != nil {
			glog.Errorf("unexpected error: %v", err)
		}
		glog.Infof("NGINX configuration: %v", string(b))
	}

	err := t.tmpl.Execute(t.tmplBuf, conf)
	if err != nil && err.Error() != errNoChild {
		return nil, err
	}

	// squeezes multiple adjacent empty lines to be single
	// spaced this is to avoid the use of regular expressions
	path := os.Getenv("GOPATH")
	path += cleanScript
	cmd := exec.Command(path)
	cmd.Stdin = t.tmplBuf
	cmd.Stdout = t.outCmdBuf
	if err := cmd.Run(); err != nil {
		if err.Error() != errNoChild {
			glog.Warningf("unexpected error cleaning template: %v", err)
		}

		return t.tmplBuf.Bytes(), nil
	}

	return t.outCmdBuf.Bytes(), nil
}

var (
	funcMap = text_template.FuncMap{
		"empty": func(input interface{}) bool {
			check, ok := input.(string)
			if ok {
				return len(check) == 0
			}
			return true
		},
		"buildLogFormatUpstream": buildLogFormatUpstream,
		"contains":               strings.Contains,
		"hasPrefix":              strings.HasPrefix,
		"hasSuffix":              strings.HasSuffix,
		"toUpper":                strings.ToUpper,
		"toLower":                strings.ToLower,
		"hasString": func(role string, feature string) bool {
			if len(role) == 0 {
				return false
			}
			if strings.Contains(role, feature) {
				return true
			}
			return false
		},
	}
)

func buildLogFormatUpstream(input interface{}) string {
	cfg, ok := input.(types.Configuration)
	if !ok {
		glog.Errorf("error  an ingress.buildLogFormatUpstream type but %T was returned", input)
	}

	return cfg.BuildLogFormatUpstream()
}

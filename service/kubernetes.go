// Copyright (c) 2016 Pulcy.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package service

import (
	"github.com/YakLabs/k8s-client/http"
	"github.com/juju/errgo"
	"github.com/op/go-logging"
)

// UpdateConfigFromKubernetes fills the AnnounceIP & Port from kubernetes.
func UpdateConfigFromKubernetes(log *logging.Logger, cfg ServiceConfig, namespace, podName string) (ServiceConfig, error) {
	if cfg.AnnounceIP != "" && cfg.AnnouncePort != 0 {
		return cfg, nil
	}
	if namespace == "" || podName == "" {
		return cfg, nil
	}

	c, err := http.NewInCluster()
	if err != nil {
		return cfg, maskAny(err)
	}

	// Fetch pod
	pod, err := c.GetPod(namespace, podName)
	if err != nil {
		return cfg, maskAny(errgo.Notef(err, "failed to get pod '%s': %v", podName, err))
	}

	cfg.AnnounceIP = pod.Status.PodIP
	log.Debugf("using AnnounceIP '%s'", cfg.AnnounceIP)
	cfg.AnnouncePort = cfg.SyncPort
	log.Debugf("using AccountPort %d", cfg.AnnouncePort)
	return cfg, nil
}

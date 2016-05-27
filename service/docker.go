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
	"fmt"
	"strconv"

	docker "github.com/fsouza/go-dockerclient"
	"github.com/op/go-logging"
)

// UpdateConfigFromDocker fills the AnnounceIP & Port from docker.
func UpdateConfigFromDocker(log *logging.Logger, cfg ServiceConfig) (ServiceConfig, error) {
	if cfg.AnnounceIP != "" && cfg.AnnouncePort != 0 {
		return cfg, nil
	}
	if cfg.DockerEndpoint == "" || cfg.ContainerID == "" {
		return cfg, nil
	}

	client, err := docker.NewClient(cfg.DockerEndpoint)
	if err != nil {
		return cfg, maskAny(err)
	}
	log.Debugf("inspecting container '%s'", cfg.ContainerID)
	container, err := client.InspectContainer(cfg.ContainerID)
	if err != nil {
		return cfg, maskAny(err)
	}
	ns := container.NetworkSettings
	if ns == nil {
		return cfg, maskAny(fmt.Errorf("No NetworkSettings found in container '%s'", cfg.ContainerID))
	}
	port := docker.Port(fmt.Sprintf("%d/tcp", cfg.SyncPort))
	bindings, ok := ns.Ports[port]
	if !ok || len(bindings) == 0 {
		return cfg, maskAny(fmt.Errorf("Port '%s' not found in NetworkSettings of '%s'", port, cfg.ContainerID))
	}
	if cfg.AnnounceIP == "" {
		cfg.AnnounceIP = bindings[0].HostIP
		log.Debugf("using AccountIP '%s'", cfg.AnnounceIP)
	}
	if cfg.AnnouncePort == 0 {
		cfg.AnnouncePort, err = strconv.Atoi(bindings[0].HostPort)
		if err != nil {
			return cfg, maskAny(err)
		}
		log.Debugf("using AccountPort %d", cfg.AnnouncePort)
	}
	return cfg, nil
}

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

package backend

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	k8s "github.com/YakLabs/k8s-client"
	"github.com/YakLabs/k8s-client/http"
	"github.com/juju/errgo"
	"github.com/op/go-logging"
)

const (
	announceLabel                  = "pulcy.com/correlation"
	announceRegistrationAnnotation = "pulcy_com_correlation_registration"
)

type k8sBackend struct {
	client              k8s.Client
	namespace           string
	podName             string
	labelSelectionValue string
	Logger              *logging.Logger
	lastState           string
	last                DeviceRegistrations

	announceMutex    sync.Mutex
	announceStarted  bool
	announceDeviceID string
	announceAddress  string
}

func NewKubernetesBackend(logger *logging.Logger, namespace, podName, labelSelectionValue string) (Backend, error) {
	c, err := http.NewInCluster()
	if err != nil {
		return nil, maskAny(err)
	}
	if namespace == "" {
		return nil, maskAny(fmt.Errorf("namespace not set"))
	}
	if podName == "" {
		return nil, maskAny(fmt.Errorf("podName not set"))
	}
	if labelSelectionValue == "" {
		return nil, maskAny(fmt.Errorf("labelSelectionValue not set"))
	}

	// Fetch pod
	if _, err := c.GetPod(namespace, podName); err != nil {
		return nil, maskAny(errgo.Notef(err, "failed to get pod '%s': %v", podName, err))
	}

	return &k8sBackend{
		client:              c,
		namespace:           namespace,
		podName:             podName,
		labelSelectionValue: labelSelectionValue,
		Logger:              logger,
	}, nil
}

// Watch for changes on a path and return where there is a change.
func (b *k8sBackend) Watch() error {
	for {
		regs, err := b.get()
		if err != nil {
			b.Logger.Warningf("failed to get registrations: %v", err)
		} else {
			state := regs.FullString()
			if state != b.lastState {
				// There is a change
				b.lastState = state
				b.last = regs
				return nil
			}
		}
		time.Sleep(time.Second * 5)
	}
}

// Load all registered devices
func (b *k8sBackend) Get() (DeviceRegistrations, error) {
	if regs := b.last; regs != nil {
		return regs, nil
	}
	regs, err := b.get()
	if err != nil {
		return nil, maskAny(err)
	}
	b.last = regs
	return regs, nil
}

// Announce the given device that can be found at the given address
func (b *k8sBackend) Announce(deviceID, address string) {
	if deviceID == "" || address == "" {
		b.UnAnnounce()
		return
	}

	// Create registration
	reg, err := b.parseAddress(address)
	if err != nil {
		b.Logger.Errorf("Failed to parse address: %#v", err)
		return
	}
	reg.ID = deviceID

	// Create JSON
	raw, err := json.Marshal(reg)
	if err != nil {
		b.Logger.Errorf("Failed to marshal registration to JSON: %#v", err)
		return
	}

	// Record in Kubernetes data
	b.updateAnnounce(string(raw))
}

// UnAnnounce removes the current announcement
func (b *k8sBackend) UnAnnounce() {
	b.Logger.Debugf("removing device announcements")
	b.updateAnnounce("")
}

// Announce the configured device that can be found at the configured address
func (b *k8sBackend) updateAnnounce(annotationValue string) {
	for i := 0; i < 25; i++ {
		if err := b.updateAnnounceOnce(annotationValue); err != nil {
			b.Logger.Warningf("Failed to update announcement: %#v", err)
			time.Sleep(time.Second * 2)
		} else {
			// We're done
			return
		}
	}
}

// Announce the configured device that can be found at the configured address
func (b *k8sBackend) updateAnnounceOnce(annotationValue string) error {
	pod, err := b.client.GetPod(b.namespace, b.podName)
	if err != nil {
		return maskAny(err)
	}

	// Update labels
	if pod.ObjectMeta.Labels == nil {
		pod.ObjectMeta.Labels = make(map[string]string)
	}
	pod.ObjectMeta.Labels[announceLabel] = b.labelSelectionValue

	// Update annotations
	if pod.ObjectMeta.Annotations == nil {
		pod.ObjectMeta.Annotations = make(map[string]string)
	}
	pod.ObjectMeta.Annotations[announceRegistrationAnnotation] = annotationValue

	// Update pod
	if _, err := b.client.UpdatePod(b.namespace, pod); err != nil {
		return maskAny(err)
	}
	return nil
}

// Load all registered devices
func (b *k8sBackend) get() (DeviceRegistrations, error) {
	// Find all matching pods
	options := &k8s.ListOptions{
		LabelSelector: k8s.LabelSelector{
			MatchLabels: map[string]string{
				announceLabel: b.labelSelectionValue,
			},
		},
	}
	pods, err := b.client.ListPods(b.namespace, options)
	if err != nil {
		return nil, maskAny(err)
	}

	var regs DeviceRegistrations
	for _, pod := range pods.Items {
		ann, ok := pod.ObjectMeta.Annotations[announceRegistrationAnnotation]
		if !ok || ann == "" {
			b.Logger.Debugf("Pod %s has no registration annotation", pod.Name)
			continue
		}
		var reg DeviceRegistration
		if err := json.Unmarshal([]byte(ann), &reg); err != nil {
			b.Logger.Debugf("Failed to parse registration annotation from pod %s: %#v", pod.Name, err)
			continue
		}
		regs = append(regs, reg)
	}

	return regs, nil
}

// parseAddress parses a string in the format of "<ip>':'<port>" into a DeviceRegistration.
func (b *k8sBackend) parseAddress(s string) (DeviceRegistration, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return DeviceRegistration{}, maskAny(fmt.Errorf("Invalid device '%s'", s))
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil {
		return DeviceRegistration{}, maskAny(fmt.Errorf("Invalid device port '%s' in '%s'", parts[1], s))
	}
	return DeviceRegistration{
		IP:   parts[0],
		Port: port,
	}, nil
}

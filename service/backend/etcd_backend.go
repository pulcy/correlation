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
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/etcd/client"
	"github.com/op/go-logging"
	"golang.org/x/net/context"
)

const (
	recentWatchErrorsMax = 5
)

var (
	announceTTL = time.Second * 60
)

type etcdBackend struct {
	client            client.Client
	watcher           client.Watcher
	Logger            *logging.Logger
	devicesKey        string
	recentWatchErrors int
	announceMutex     sync.Mutex
	announceStarted   bool
	announceDeviceID  string
	announceAddress   string
}

func NewEtcdBackend(logger *logging.Logger, endpoints []string, path string) (Backend, error) {
	cfg := client.Config{
		Transport: client.DefaultTransport,
		Endpoints: endpoints,
	}
	c, err := client.New(cfg)
	if err != nil {
		return nil, maskAny(err)
	}
	keysAPI := client.NewKeysAPI(c)
	options := &client.WatcherOptions{
		Recursive: true,
	}
	watcher := keysAPI.Watcher(path, options)

	go c.AutoSync(context.Background(), time.Minute)

	return &etcdBackend{
		client:     c,
		watcher:    watcher,
		devicesKey: path,
		Logger:     logger,
	}, nil
}

// Watch for changes on a path and return where there is a change.
func (eb *etcdBackend) Watch() error {
	if eb.watcher == nil || eb.recentWatchErrors > recentWatchErrorsMax {
		eb.recentWatchErrors = 0
		keysAPI := client.NewKeysAPI(eb.client)
		options := &client.WatcherOptions{
			Recursive: true,
		}
		eb.watcher = keysAPI.Watcher(eb.devicesKey, options)
	}
	_, err := eb.watcher.Next(context.Background())
	if err != nil {
		eb.recentWatchErrors++
		return maskAny(err)
	}
	eb.recentWatchErrors = 0
	return nil
}

// Load all registered devices
func (eb *etcdBackend) Get() (DeviceRegistrations, error) {
	devices, err := eb.readDevicesTree()
	if err != nil {
		return nil, maskAny(err)
	}
	return devices, nil
}

// Announce the given device that can be found at the given address
func (eb *etcdBackend) Announce(deviceID, address string) {
	eb.announceMutex.Lock()
	defer eb.announceMutex.Unlock()
	eb.announceDeviceID = deviceID
	eb.announceAddress = address

	if deviceID == "" || address == "" {
		return
	}

	if !eb.announceStarted {
		go eb.announceLoop()
		eb.announceStarted = true
	}
}

// UnAnnounce removes the current announcement
func (eb *etcdBackend) UnAnnounce() {
	eb.announceMutex.Lock()
	defer eb.announceMutex.Unlock()
	deviceID := eb.announceDeviceID
	eb.announceDeviceID = ""
	eb.announceAddress = ""

	if deviceID != "" {
		keysAPI := client.NewKeysAPI(eb.client)
		key := path.Join(eb.devicesKey, deviceID)
		eb.Logger.Debugf("removing device announcements at '%s'", key)
		_, err := keysAPI.Delete(context.Background(), key, nil)
		if err != nil {
			eb.Logger.Warningf("Failed to delete %s key: %#v", key, err)
		}
	}
}

// announceLoop keeps updating our announcement
func (eb *etcdBackend) announceLoop() error {
	for {
		if err := eb.updateAnnounce(); err != nil {
			eb.Logger.Warningf("Update announce failed: %#v", err)
			time.Sleep(time.Second)
		} else {
			time.Sleep(announceTTL / 2)
		}
	}
}

// Announce the configured device that can be found at the configured address
func (eb *etcdBackend) updateAnnounce() error {
	eb.announceMutex.Lock()
	defer eb.announceMutex.Unlock()
	deviceID := eb.announceDeviceID
	address := eb.announceAddress

	if deviceID == "" || address == "" {
		return nil
	}

	keysAPI := client.NewKeysAPI(eb.client)
	options := &client.SetOptions{
		TTL: announceTTL,
	}
	key := path.Join(eb.devicesKey, deviceID)
	_, err := keysAPI.Set(context.Background(), key, address, options)
	if err != nil {
		return maskAny(err)
	}
	return nil
}

// Load all registered devices
func (eb *etcdBackend) readDevicesTree() (DeviceRegistrations, error) {
	keysAPI := client.NewKeysAPI(eb.client)
	options := &client.GetOptions{
		Recursive: true,
		Sort:      false,
	}
	resp, err := keysAPI.Get(context.Background(), eb.devicesKey, options)
	if err != nil {
		return nil, maskAny(err)
	}
	result := DeviceRegistrations{}
	if resp.Node == nil {
		return result, nil
	}
	for _, instanceNode := range resp.Node.Nodes {
		uniqueID := path.Base(instanceNode.Key)
		dev, err := eb.parseDeviceInfo(instanceNode.Value)
		if err != nil {
			eb.Logger.Warning("Failed to parse device '%s': %#v", instanceNode.Value, err)
			continue
		}

		dev.ID = uniqueID
		result = append(result, dev)
	}

	return result, nil
}

// parseDeviceInfo parses a string in the format of "<ip>':'<port>" into a DeviceRegistration.
func (eb *etcdBackend) parseDeviceInfo(s string) (DeviceRegistration, error) {
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

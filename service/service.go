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
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dchest/uniuri"
	"github.com/op/go-logging"
	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/protocol"

	"github.com/pulcy/correlation/service/backend"
	"github.com/pulcy/correlation/syncthing"
)

const (
	osExitDelay      = time.Second * 3
	confPerm         = os.FileMode(0664) // rw-rw-r
	refreshDelay     = time.Millisecond * 250
	syncthingAddress = "http://127.0.0.1:2231"
	rescanIntervalS  = 30
	folderID         = "1338de53-f75a-4164-b769-dd62c1273717"
	folderLabel      = "sync-dir"
)

type ServiceConfig struct {
	LocalIP   string // IP address on which I'm reachable
	LocalPort int    // Port number on which I'm reachable

	SyncthingPath string // Full path of syncthing binary
	SyncDir       string // Full path of directory to synchronize
	ConfigDir     string // Full path of directory to use as home/configuration directory
}

type ServiceDependencies struct {
	Logger  *logging.Logger
	Backend backend.Backend
}

type Service struct {
	ServiceConfig
	ServiceDependencies

	signalCounter uint32
	lastConfig    string
	lastPid       int
	changeCounter uint32
	apiKey        string
	syncClient    *syncthing.Client
}

// NewService creates a new service instance.
func NewService(config ServiceConfig, deps ServiceDependencies) *Service {
	if config.SyncthingPath == "" {
		config.SyncthingPath = "/app/syncthing"
	}
	apiKey := uniuri.New()
	syncClient := syncthing.NewClient(syncthing.ClientConfig{
		Endpoint:           syncthingAddress,
		APIKey:             apiKey,
		InsecureSkipVerify: false,
	})
	return &Service{
		ServiceConfig:       config,
		ServiceDependencies: deps,
		apiKey:              apiKey,
		syncClient:          syncClient,
	}
}

// Run starts the service and waits for OS signals to terminate it.
func (s *Service) Run() error {
	if err := s.generateConfig(); err != nil {
		return maskAny(err)
	}

	// Run syncthing
	go s.runSyncthing()

	// Start monitoring the backend
	go s.backendMonitorLoop()

	// Update when needed
	go s.configLoop()

	// Trigger initial update
	go func() {
		time.Sleep(time.Millisecond * 100)
		s.TriggerUpdate()
	}()
	s.listenSignals()

	return nil
}

// configLoop updates the haproxy config, and then waits
// for changes in the backend.
func (s *Service) configLoop() {
	var lastChangeCounter uint32
	for {
		currentChangeCounter := atomic.LoadUint32(&s.changeCounter)
		if currentChangeCounter > lastChangeCounter {
			if err := s.updateSyncthing(); err != nil {
				s.Logger.Errorf("Failed to update syncthing: %#v", err)
			} else {
				// Success
				lastChangeCounter = currentChangeCounter
			}
		}
		select {
		case <-time.After(refreshDelay):
		}
	}
}

// backendMonitorLoop monitors the configuration backend for changes.
// When it detects a change, it set a dirty flag.
func (s *Service) backendMonitorLoop() {
	for {
		if err := s.Backend.Watch(); err != nil {
			s.Logger.Errorf("Failed to watch for backend changes: %#v", err)
		}
		s.TriggerUpdate()
	}
}

// TriggerUpdate notifies the service to update the haproxy configuration
func (s *Service) TriggerUpdate() {
	atomic.AddUint32(&s.changeCounter, 1)
}

// update the syncthing configuration
func (s *Service) updateSyncthing() error {
	// Load current configuration
	cfg, err := s.syncClient.GetConfig()
	if err != nil {
		return maskAny(err)
	}

	// Load devices from backend
	devices, err := s.Backend.Get()
	if err != nil {
		return maskAny(err)
	}

	// Update config
	fld := config.FolderConfiguration{
		ID:              folderID,
		Label:           folderLabel,
		RawPath:         s.SyncDir,
		Type:            config.FolderTypeReadWrite,
		RescanIntervalS: rescanIntervalS,
	}
	cfg.Devices = []config.DeviceConfiguration{}
	for _, dr := range devices {
		devID, err := protocol.DeviceIDFromString(dr.ID)
		if err != nil {
			return maskAny(err)
		}
		dev := config.DeviceConfiguration{
			DeviceID:    devID,
			Name:        dr.ID,
			Addresses:   []string{fmt.Sprintf("%s:%d", dr.IP, dr.Port)},
			Compression: protocol.CompressAlways,
		}
		cfg.Devices = append(cfg.Devices, dev)
		fld.Devices = append(fld.Devices, config.FolderDeviceConfiguration{DeviceID: devID})
	}
	cfg.Folders = []config.FolderConfiguration{fld}

	// Store updated config
	if err := s.syncClient.SetConfig(cfg); err != nil {
		return maskAny(err)
	}

	s.Logger.Info("Updated syncthing")

	return nil
}

// generateConfig calls syncthing to generate a configuration file.
func (s *Service) generateConfig() error {
	cmd := exec.Command(s.SyncthingPath, "-generate", "-home", s.ConfigDir)
	cmd.Stdin = bytes.NewReader([]byte{})
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		s.Logger.Errorf("Error generating syncthing config: %#v", err)
		return maskAny(err)
	}
	return nil
}

// runSyncthing runs syncthing for normal operations
func (s *Service) runSyncthing() error {
	args := []string{
		"-home=" + s.ConfigDir,
		"-no-browser",
		"-no-restart",
		"-gui-apikey=" + s.apiKey,
	}

	s.Logger.Debugf("Starting syncthing with %#v", args)
	cmd := exec.Command(s.SyncthingPath, args...)
	cmd.Stdin = bytes.NewReader([]byte{})
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		s.Logger.Errorf("Failed to start syncthing: %#v", err)
		return maskAny(err)
	}

	s.Logger.Debug("syncthing started, waiting for finish...")
	if err := cmd.Wait(); err != nil {
		s.Logger.Errorf("syncthing wait returned an error: %#v", err)
	} else {
		s.Logger.Debug("syncthing terminated")
	}

	return nil
}

// close closes this service in a timely manor.
func (s *Service) close() {
	// Interrupt the process when closing is requested twice.
	if atomic.AddUint32(&s.signalCounter, 1) >= 2 {
		s.exitProcess()
	}

	s.Logger.Infof("shutting down server in %s", osExitDelay.String())
	time.Sleep(osExitDelay)

	s.exitProcess()
}

// exitProcess terminates this process with exit code 1.
func (s *Service) exitProcess() {
	s.Logger.Info("shutting down server")
	os.Exit(0)
}

// listenSignals waits for incoming OS signals and acts upon them
func (s *Service) listenSignals() {
	// Set up channel on which to send signal notifications.
	// We must use a buffered channel or risk missing the signal
	// if we're not ready to receive when the signal is sent.
	c := make(chan os.Signal, 2)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)

	// Block until a signal is received.
	for {
		select {
		case sig := <-c:
			s.Logger.Infof("server received signal %s", sig)
			go s.close()
		}
	}
}

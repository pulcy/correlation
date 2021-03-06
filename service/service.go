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
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/dchest/uniuri"
	"github.com/op/go-logging"
	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/protocol"

	"github.com/pulcy/correlation/service/backend"
	"github.com/pulcy/correlation/syncthing"
)

const (
	osExitDelay  = time.Second * 3
	confPerm     = os.FileMode(0664) // rw-rw-r
	refreshDelay = time.Millisecond * 250
	folderID     = "1338de53-f75a-4164-b769-dd62c1273717"
	folderLabel  = "sync-dir"
)

type ServiceConfig struct {
	SyncPort int // Port number for syncthing to listen on
	HttpPort int // Port number for syncthing GUI & REST API to listen on

	DockerEndpoint string // If set, fetches the announce IP & port from docker
	ContainerID    string // If set, fetches the announce IP & port from docker
	AnnounceIP     string // IP address on which I'm reachable
	AnnouncePort   int    // Port number on which I'm reachable

	SyncthingPath string // Full path of syncthing binary
	SyncDir       string // Full path of directory to synchronize
	ConfigDir     string // Full path of directory to use as home/configuration directory
	Master        bool   // If set, my folder will be readonly and not accept changes from others

	RescanInterval time.Duration // Amount of time bewteen scans
	NoWatcher      bool          // If set, no watcher will be launched.

	User     string // User for GUI access
	Password string // Password for GUI access
}

type ServiceDependencies struct {
	Logger        *logging.Logger
	WatcherLogger *logging.Logger
	Backend       backend.Backend
}

type Service struct {
	ServiceConfig
	ServiceDependencies

	signalCounter  uint32
	lastDevices    string
	changeCounter  uint32
	apiKey         string
	syncClient     *syncthing.Client
	watcher        *Watcher
	watcherRunOnce sync.Once
}

// NewService creates a new service instance.
func NewService(config ServiceConfig, deps ServiceDependencies) (*Service, error) {
	// Apply defaults
	if config.AnnouncePort == 0 {
		config.AnnouncePort = config.SyncPort
	}
	if config.SyncthingPath == "" {
		config.SyncthingPath = "/app/syncthing"
	}
	// Check input
	if config.AnnounceIP == "" {
		return nil, maskAny(fmt.Errorf("AnnounceIP is empty"))
	}
	if config.AnnouncePort == 0 {
		return nil, maskAny(fmt.Errorf("AnnouncePort is 0"))
	}
	if config.SyncDir == "" {
		return nil, maskAny(fmt.Errorf("SyncDir is empty"))
	}
	if config.ConfigDir == "" {
		return nil, maskAny(fmt.Errorf("ConfigDir is empty"))
	}
	apiKey := uniuri.New()
	s := &Service{
		ServiceConfig:       config,
		ServiceDependencies: deps,
		apiKey:              apiKey,
	}
	if err := s.generateConfig(); err != nil {
		return nil, maskAny(err)
	}
	s.syncClient = syncthing.NewClient(syncthing.ClientConfig{
		Endpoint:           fmt.Sprintf("http://127.0.0.1:%d", config.HttpPort),
		APIKey:             apiKey,
		InsecureSkipVerify: false,
	})
	if !config.NoWatcher {
		s.watcher = NewWatcher(deps.WatcherLogger, s.syncClient, folderID, config.SyncDir)
	}
	return s, nil
}

// Run starts the service and waits for OS signals to terminate it.
func (s *Service) Run() error {
	// Set env variables
	os.Setenv("STNOUPGRADE", "true")

	// Run syncthing
	go s.runSyncthing()

	// Fetch my device ID
	s.Logger.Debug("waiting for syncthing to get started")
	deviceID := s.waitAndGetLocalDeviceID()

	// Announce us in the backend
	announceAddress := net.JoinHostPort(s.AnnounceIP, strconv.Itoa(s.AnnouncePort))
	s.Logger.Debugf("announcing me at %s", announceAddress)
	s.Backend.Announce(deviceID, announceAddress)

	// Start monitoring the backend
	go s.backendMonitorLoop()

	// Update when needed
	go s.configLoop()

	// Trigger initial update
	s.TriggerUpdate()

	// Wait for incoming signals
	s.listenSignals()

	return nil
}

// waitAndGetLocalDeviceID fetched the device ID of the local syncthing instance.
// It waits until it gets a successful response.
func (s *Service) waitAndGetLocalDeviceID() string {
	var id string
	op := func() error {
		var err error
		id, err = s.syncClient.GetMyID()
		if err != nil {
			return maskAny(err)
		} else if id == "" {
			return maskAny(fmt.Errorf("id empty"))
		}
		return nil
	}
	backoff.Retry(op, backoff.NewExponentialBackOff())
	return id
}

// waitForConfigInSync waits until the syncthing config is in sync with what is on disk.
func (s *Service) waitForConfigInSync() {
	op := func() error {
		if inSync, err := s.syncClient.IsConfigInSync(); err != nil {
			return maskAny(err)
		} else if !inSync {
			return maskAny(fmt.Errorf("not in sync"))
		}
		return nil
	}
	backoff.Retry(op, backoff.NewExponentialBackOff())
}

// runWatcher starts the watcher (if any)
func (s *Service) runWatcher() {
	// Watch for changes
	if s.watcher != nil {
		s.Logger.Debugf("starting watcher")
		go s.watcher.Run()
	}
}

// configLoop updates the haproxy config, and then waits
// for changes in the backend.
func (s *Service) configLoop() {
	var lastChangeCounter uint32
	for {
		currentChangeCounter := atomic.LoadUint32(&s.changeCounter)
		signalCounter := atomic.LoadUint32(&s.signalCounter)

		if currentChangeCounter > lastChangeCounter && signalCounter == 0 {
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
	devicesString := devices.FullString()
	if devicesString == s.lastDevices {
		// No update needed
		return nil
	}

	// Update config
	fld := config.FolderConfiguration{
		ID:              folderID,
		Label:           folderLabel,
		RawPath:         s.SyncDir,
		Type:            config.FolderTypeReadWrite,
		RescanIntervalS: (int)(s.RescanInterval.Seconds()),
	}
	if s.Master {
		fld.Type = config.FolderTypeReadOnly
	}
	scheme := "tcp"
	cfg.GUI.RawAddress = fmt.Sprintf(":%d", s.HttpPort)
	cfg.GUI.RawUseTLS = false
	cfg.GUI.APIKey = s.apiKey
	cfg.GUI.User = s.User
	cfg.GUI.Password = s.Password
	cfg.Options.ListenAddresses = []string{fmt.Sprintf("tcp://:%d", s.SyncPort)}
	cfg.Options.GlobalAnnEnabled = false
	cfg.Options.GlobalAnnServers = []string{}
	cfg.Options.LocalAnnEnabled = false
	cfg.Options.NATEnabled = false
	cfg.Options.RelaysEnabled = false
	cfg.Options.StartBrowser = false
	cfg.Options.URAccepted = -1 // -1 for off (permanently)
	cfg.Devices = []config.DeviceConfiguration{}
	for _, dr := range devices {
		devID, err := protocol.DeviceIDFromString(dr.ID)
		if err != nil {
			return maskAny(err)
		}
		dev := config.DeviceConfiguration{
			DeviceID:    devID,
			Name:        dr.ID,
			Addresses:   []string{fmt.Sprintf("%s://%s:%d", scheme, dr.IP, dr.Port)},
			Compression: protocol.CompressMetadata,
		}
		cfg.Devices = append(cfg.Devices, dev)
		fld.Devices = append(fld.Devices, config.FolderDeviceConfiguration{DeviceID: devID})
	}
	cfg.Folders = []config.FolderConfiguration{fld}

	// Store updated config
	if err := s.syncClient.SetConfig(cfg); err != nil {
		return maskAny(err)
	}
	if err := s.syncClient.Restart(); err != nil {
		return maskAny(err)
	}
	s.Logger.Debug("waiting for config to be insync")
	s.waitForConfigInSync()

	s.Logger.Info("Updated syncthing")
	s.lastDevices = devicesString

	// Activate watcher (if needed)
	s.watcherRunOnce.Do(s.runWatcher)

	return nil
}

// generateConfig calls syncthing to generate a configuration file.
func (s *Service) generateConfig() error {
	args := []string{
		"-generate=" + s.ConfigDir,
		"-home=" + s.ConfigDir,
		"-no-browser",
		"-gui-apikey=" + s.apiKey,
		"-gui-address=" + fmt.Sprintf(":%d", s.HttpPort),
	}
	s.Logger.Debugf("Starting syncthing (generate) with %#v", args)
	cmd := exec.Command(s.SyncthingPath, args...)
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
		"-gui-apikey=" + s.apiKey,
		"-gui-address=" + fmt.Sprintf(":%d", s.HttpPort),
	}

	s.Logger.Debugf("Starting syncthing (run) with %#v", args)
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

	// Remove announcement
	s.Backend.UnAnnounce()

	// Close watcher (if any)
	if s.watcher != nil {
		s.watcher.Close()
	}

	// Shutdown syncthing
	go s.syncClient.Shutdown()

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

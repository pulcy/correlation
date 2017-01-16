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

package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/juju/errgo"
	"github.com/op/go-logging"
	"github.com/spf13/cobra"

	"github.com/pulcy/correlation/service"
	"github.com/pulcy/correlation/service/backend"
)

var (
	projectVersion = "dev"
	projectBuild   = "dev"

	maskAny = errgo.MaskFunc(errgo.Any)
)

const (
	projectName           = "correlation"
	defaultHttpPort       = 5812
	defaultSyncPort       = 5808
	defaultRescanInterval = time.Second * 30
	defaultDockerEndpoint = "unix:///var/run/docker.sock"

	defaultLogLevel        = "debug"
	defaultWatcherLogLevel = "info"
	backendLogName         = "service"
	serviceLogName         = "service"
	watcherLogName         = "watcher"
)

type globalOptions struct {
	logLevel            string
	backendLogLevel     string
	serviceLogLevel     string
	watcherLogLevel     string
	etcdAddr            string
	etcdEndpoints       []string
	etcdPath            string
	kubernetesNamespace string
	kubernetesPodName   string
	service.ServiceConfig
}

var (
	cmdMain = &cobra.Command{
		Use: projectName,
		Run: cmdMainRun,
	}
	globalFlags globalOptions
)

func init() {
	logging.SetFormatter(logging.MustStringFormatter("[%{level:-5s}] %{message}"))

	cmdMain.Flags().StringVar(&globalFlags.logLevel, "log-level", defaultLogLevel, "Minimum log level (debug|info|warning|error)")
	cmdMain.Flags().StringVar(&globalFlags.backendLogLevel, "backend-log-level", "", "Minimum log level (debug|info|warning|error)")
	cmdMain.Flags().StringVar(&globalFlags.serviceLogLevel, "service-log-level", "", "Minimum log level (debug|info|warning|error)")
	cmdMain.Flags().StringVar(&globalFlags.watcherLogLevel, "watcher-log-level", defaultWatcherLogLevel, "Minimum log level for the filesystem watcher (debug|info|warning|error)")
	// ETCD backend
	cmdMain.Flags().StringVar(&globalFlags.etcdAddr, "etcd-addr", "", "Address of etcd backend")
	cmdMain.Flags().StringSliceVar(&globalFlags.etcdEndpoints, "etcd-endpoint", nil, "Etcd client endpoints")
	cmdMain.Flags().StringVar(&globalFlags.etcdPath, "etcd-path", "", "Path into etcd namespace")
	// Kubernetes backend
	cmdMain.Flags().StringVar(&globalFlags.kubernetesNamespace, "kubernetes-namespace", "", "Namespace of current pod in Kubernetes")
	cmdMain.Flags().StringVar(&globalFlags.kubernetesPodName, "kubernetes-podname", "", "Name of current pod in Kubernetes")

	cmdMain.Flags().IntVar(&globalFlags.SyncPort, "sync-port", defaultSyncPort, "Port for syncthing to listen on")
	cmdMain.Flags().IntVar(&globalFlags.HttpPort, "http-port", defaultHttpPort, "Port for syncthing's GUI & REST to listen on")
	cmdMain.Flags().StringVar(&globalFlags.AnnounceIP, "announce-ip", "", "IP address to announce to other instances")
	cmdMain.Flags().IntVar(&globalFlags.AnnouncePort, "announce-port", 0, "Port to announce to other instances")
	cmdMain.Flags().StringVar(&globalFlags.SyncthingPath, "syncthing-path", "", "Path of syncthing")
	cmdMain.Flags().StringVar(&globalFlags.SyncDir, "sync-dir", "", "Path of the directory to synchronize")
	cmdMain.Flags().StringVar(&globalFlags.ConfigDir, "config-dir", "", "Path of the directory containing the configuration")
	cmdMain.Flags().StringVar(&globalFlags.User, "gui-user", "", "Username for accessing the GUI")
	cmdMain.Flags().StringVar(&globalFlags.Password, "gui-password", "", "Password for accessing the GUI")
	cmdMain.Flags().DurationVar(&globalFlags.RescanInterval, "rescan-interval", defaultRescanInterval, "Time between scans of the sync-dir")
	cmdMain.Flags().BoolVar(&globalFlags.Master, "master", false, "If set my folder will be considered the master and will not receive updates from others")
	cmdMain.Flags().StringVar(&globalFlags.DockerEndpoint, "docker-endpoint", defaultDockerEndpoint, "Where to access docker")
	cmdMain.Flags().StringVar(&globalFlags.ContainerID, "container", "", "ID of the container running this process")
	cmdMain.Flags().BoolVar(&globalFlags.NoWatcher, "no-watcher", false, "If set, no filesystem watcher is launched and only timer based scanning is used")
}

func main() {
	cmdMain.Execute()
}

func cmdMainRun(cmd *cobra.Command, args []string) {
	// Parse arguments
	if globalFlags.etcdAddr != "" {
		etcdUrl, err := url.Parse(globalFlags.etcdAddr)
		if err != nil {
			Exitf("--etcd-addr '%s' is not valid: %#v", globalFlags.etcdAddr, err)
		}
		globalFlags.etcdEndpoints = []string{fmt.Sprintf("%s://%s", etcdUrl.Scheme, etcdUrl.Host)}
		globalFlags.etcdPath = etcdUrl.Path
	}

	// Set log levels
	setLogLevel(globalFlags.backendLogLevel, globalFlags.logLevel, backendLogName)
	setLogLevel(globalFlags.serviceLogLevel, globalFlags.logLevel, serviceLogName)
	setLogLevel(globalFlags.watcherLogLevel, globalFlags.logLevel, watcherLogName)

	// Prepare backend
	var b backend.Backend
	useKubernetes := false
	var err error
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" && os.Getenv("KUBERNETES_SERVICE_PORT") != "" &&
		globalFlags.kubernetesNamespace != "" && globalFlags.kubernetesPodName != "" {
		// Looks like we're running in Kubernetes, use the Kubernetes backend
		labelSelectionValue := strings.TrimPrefix(strings.Replace(globalFlags.etcdPath, "/", "_", -1), "_")
		b, err = backend.NewKubernetesBackend(logging.MustGetLogger(backendLogName), globalFlags.kubernetesNamespace, globalFlags.kubernetesPodName, labelSelectionValue)
		if err != nil {
			Exitf("Failed to create Kubernetes backend: %#v", err)
		}
		useKubernetes = true
	} else {
		b, err = backend.NewEtcdBackend(logging.MustGetLogger(backendLogName), globalFlags.etcdEndpoints, globalFlags.etcdPath)
		if err != nil {
			Exitf("Failed to create ETCD backend: %#v", err)
		}
	}

	// Update service config (if needed)
	serviceLogger := logging.MustGetLogger(serviceLogName)
	cfg := globalFlags.ServiceConfig
	if useKubernetes {
		cfg, err = service.UpdateConfigFromKubernetes(serviceLogger, globalFlags.ServiceConfig, globalFlags.kubernetesNamespace, globalFlags.kubernetesPodName)
		if err != nil {
			Exitf("Failed to update configuration from docker: %#v", err)
		}
	} else {
		cfg, err = service.UpdateConfigFromDocker(serviceLogger, globalFlags.ServiceConfig)
		if err != nil {
			Exitf("Failed to update configuration from docker: %#v", err)
		}
	}

	// Prepare service
	service, err := service.NewService(cfg, service.ServiceDependencies{
		Logger:        serviceLogger,
		WatcherLogger: logging.MustGetLogger(watcherLogName),
		Backend:       b,
	})
	if err != nil {
		Exitf("Failed to create service: %#v", err)
	}

	if err := service.Run(); err != nil {
		Exitf("Failed to run service: %#v", err)
	}
}

func showUsage(cmd *cobra.Command, args []string) {
	cmd.Usage()
}

func Exitf(format string, args ...interface{}) {
	if !strings.HasSuffix(format, "\n") {
		format = format + "\n"
	}
	fmt.Printf(format, args...)
	fmt.Println()
	os.Exit(1)
}

func assert(err error) {
	if err != nil {
		Exitf("Assertion failed: %#v", err)
	}
}

func assertArgIsSet(arg, argKey string) {
	if arg == "" {
		Exitf("%s must be set\n", argKey)
	}
}

func setLogLevel(logLevel, defaultLogLevel, loggerName string) {
	if logLevel == "" {
		logLevel = defaultLogLevel
	}
	level, err := logging.LogLevel(logLevel)
	if err != nil {
		Exitf("Invalid log-level '%s': %#v", logLevel, err)
	}
	logging.SetLevel(level, loggerName)
}

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
	projectName     = "correlation"
	defaultLogLevel = "debug"
)

type globalOptions struct {
	logLevel string
	etcdAddr string
	service.ServiceConfig
}

var (
	cmdMain = &cobra.Command{
		Use:              projectName,
		Run:              cmdMainRun,
		PersistentPreRun: func(*cobra.Command, []string) { setLogLevel(globalFlags.logLevel) },
	}
	globalFlags globalOptions
	log         = logging.MustGetLogger(projectName)
)

func init() {
	logging.SetFormatter(logging.MustStringFormatter("[%{level:-5s}] %{message}"))

	cmdMain.Flags().StringVar(&globalFlags.logLevel, "log-level", "", "Minimum log level (debug|info|warning|error)")
	cmdMain.Flags().StringVar(&globalFlags.etcdAddr, "etcd-addr", "", "Address of etcd backend")

	cmdMain.Flags().StringVar(&globalFlags.LocalIP, "local-ip", "", "Local IP address")
	cmdMain.Flags().IntVar(&globalFlags.LocalPort, "local-port", 0, "Local port")
	cmdMain.Flags().StringVar(&globalFlags.SyncthingPath, "syncthing-path", "", "Path of syncthing")
	cmdMain.Flags().StringVar(&globalFlags.SyncDir, "sync-dir", "", "Path of the directory to synchronize")
	cmdMain.Flags().StringVar(&globalFlags.ConfigDir, "config-dir", "", "Path of the directory containing the configuration")
}

func main() {
	cmdMain.Execute()
}

func cmdMainRun(cmd *cobra.Command, args []string) {
	// Parse arguments
	if globalFlags.etcdAddr == "" {
		Exitf("Please specify --etcd-addr")
	}
	etcdUrl, err := url.Parse(globalFlags.etcdAddr)
	if err != nil {
		Exitf("--etcd-addr '%s' is not valid: %#v", globalFlags.etcdAddr, err)
	}

	// Set log level
	level, err := logging.LogLevel(globalFlags.logLevel)
	if err != nil {
		Exitf("Invalid log-level '%s': %#v", globalFlags.logLevel, err)
	}
	logging.SetLevel(level, projectName)

	// Prepare backend
	backend, err := backend.NewEtcdBackend(log, etcdUrl)
	if err != nil {
		Exitf("Failed to backend: %#v", err)
	}

	// Prepare service
	service := service.NewService(globalFlags.ServiceConfig, service.ServiceDependencies{
		Logger:  log,
		Backend: backend,
	})

	service.Run()
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

func setLogLevel(logLevel string) {
	level, err := logging.LogLevel(logLevel)
	if err != nil {
		Exitf("Invalid log-level '%s': %#v", logLevel, err)
	}
	logging.SetLevel(level, projectName)
}

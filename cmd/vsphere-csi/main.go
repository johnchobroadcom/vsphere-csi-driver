/*
Copyright 2018 The Kubernetes Authors.

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
	"context"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	csiconfig "sigs.k8s.io/vsphere-csi-driver/v3/pkg/common/config"
	"sigs.k8s.io/vsphere-csi-driver/v3/pkg/common/utils"
	"sigs.k8s.io/vsphere-csi-driver/v3/pkg/csi/service"
	"sigs.k8s.io/vsphere-csi-driver/v3/pkg/csi/service/common/commonco"
	"sigs.k8s.io/vsphere-csi-driver/v3/pkg/csi/service/logger"
	csitypes "sigs.k8s.io/vsphere-csi-driver/v3/pkg/csi/types"
)

var (
	printVersion = flag.Bool("version", false, "Print driver version and exit")

	supervisorFSSName = flag.String("supervisor-fss-name", "",
		"Name of the feature state switch configmap in supervisor cluster")
	supervisorFSSNamespace = flag.String("supervisor-fss-namespace", "",
		"Namespace of the feature state switch configmap in supervisor cluster")
	internalFSSName      = flag.String("fss-name", "", "Name of the feature state switch configmap")
	internalFSSNamespace = flag.String("fss-namespace", "", "Namespace of the feature state switch configmap")
	enableProfileServer  = flag.Bool("enable-profile-server", false, "Enable profiling endpoint for the controller.")
)

// main is ignored when this package is built as a go plug-in.
func main() {
	flag.Parse()
	if *printVersion {
		fmt.Printf("%s\n", service.Version)
		return
	}
	logType := logger.LogLevel(os.Getenv(logger.EnvLoggerLevel))
	logger.SetLoggerLevel(logType)
	ctx, log := logger.GetNewContextWithLogger()
	log.Infof("Version : %s", service.Version)

	if *enableProfileServer {
		go func() {
			log.Info("Starting the http server to expose profiling metrics..")
			err := http.ListenAndServe(":9500", nil)
			if err != nil {
				log.Fatalf("Unable to start profiling server: %s", err)
			}
		}()
	}

	// Set CO Init params.
	clusterFlavor, err := csiconfig.GetClusterFlavor(ctx)
	if err != nil {
		log.Errorf("failed retrieving the cluster flavor. Error: %v", err)
	}
	serviceMode := os.Getenv(csitypes.EnvVarMode)
	commonco.SetInitParams(ctx, clusterFlavor, &service.COInitParams, *supervisorFSSName, *supervisorFSSNamespace,
		*internalFSSName, *internalFSSNamespace, serviceMode, "")

	// If no endpoint is set then exit the program.
	CSIEndpoint := os.Getenv(csitypes.EnvVarEndpoint)
	if CSIEndpoint == "" {
		log.Error("CSI endpoint cannot be empty. Please set the env variable.")
		os.Exit(1)
	}
	log.Info("Enable logging off for vCenter sessions on exit")
	// Disconnect VC session on restart
	defer func() {
		if r := recover(); r != nil {
			log.Info("Cleaning up vc sessions")
			cleanupSessions(ctx, r)
		}
	}()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	go func() {
		for {
			sig := <-ch
			if sig == syscall.SIGTERM {
				log.Info("SIGTERM signal received")
				utils.LogoutAllvCenterSessions(ctx)
				os.Exit(0)
			}
		}
	}()

	vSphereCSIDriver := service.NewDriver()
	vSphereCSIDriver.Run(ctx, CSIEndpoint)

}

func cleanupSessions(ctx context.Context, r interface{}) {
	log := logger.GetLogger(ctx)
	log.Errorf("Observed a panic and a restart was invoked, panic: %+v", r)
	log.Info("Recovered from panic. Disconnecting the existing vc sessions.")
	utils.LogoutAllvCenterSessions(ctx)
	os.Exit(0)
}

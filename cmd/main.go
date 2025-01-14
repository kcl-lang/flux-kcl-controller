/*
Copyright 2022 The Flux authors

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
	"os"
	"time"

	flag "github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/fluxcd/cli-utils/pkg/kstatus/polling"
	"github.com/fluxcd/cli-utils/pkg/kstatus/polling/engine"
	"github.com/fluxcd/pkg/runtime/client"
	"github.com/fluxcd/pkg/runtime/events"
	"github.com/fluxcd/pkg/runtime/logger"
	"github.com/fluxcd/pkg/runtime/metrics"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1beta2 "github.com/fluxcd/source-controller/api/v1beta2"

	helper "github.com/fluxcd/pkg/runtime/controller"
	krmkcldevfluxcdv1alpha1 "github.com/kcl-lang/flux-kcl-controller/api/v1alpha1"
	"github.com/kcl-lang/flux-kcl-controller/internal/controller"
	"github.com/kcl-lang/flux-kcl-controller/internal/statusreaders"
	// +kubebuilder:scaffold:imports
)

const controllerName = "flux-kcl-controller"

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	// GitRepository
	utilruntime.Must(sourcev1.AddToScheme(scheme))
	// OCIRepository
	utilruntime.Must(sourcev1beta2.AddToScheme(scheme))
	// KCLRun
	utilruntime.Must(krmkcldevfluxcdv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var (
		metricsAddr           string
		eventsAddr            string
		requeueDependency     time.Duration
		enableLeaderElection  bool
		httpRetry             int
		defaultServiceAccount string
		logOptions            logger.Options

		clientOptions  client.Options
		kubeConfigOpts client.KubeConfigOptions

		rateLimiterOptions      helper.RateLimiterOptions
		watchOptions            helper.WatchOptions
		disallowedFieldManagers []string
	)

	flag.StringVar(&metricsAddr, "metrics-addr", ":8083", "The address the metric endpoint binds to.")
	flag.StringVar(&eventsAddr, "events-addr", "", "The address of the events receiver.")
	flag.DurationVar(&requeueDependency, "requeue-dependency", 30*time.Second, "The interval at which failing dependencies are reevaluated.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&httpRetry, "http-retry", 9, "The maximum number of retries when failing to fetch artifacts over HTTP.")
	flag.StringVar(&defaultServiceAccount, "default-service-account", "",
		"Default service account used for impersonation.")
	flag.StringArrayVar(&disallowedFieldManagers, "override-manager", []string{}, "Field manager disallowed to perform changes on managed resources.")

	clientOptions.BindFlags(flag.CommandLine)
	logOptions.BindFlags(flag.CommandLine)
	rateLimiterOptions.BindFlags(flag.CommandLine)
	kubeConfigOpts.BindFlags(flag.CommandLine)
	watchOptions.BindFlags(flag.CommandLine)

	flag.Parse()
	ctrl.SetLogger(logger.NewLogger(logOptions))
	ctx := ctrl.SetupSignalHandler()

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:           scheme,
		Metrics:          metricsserver.Options{BindAddress: metricsAddr},
		LeaderElection:   enableLeaderElection,
		LeaderElectionID: "kcl-lang.io",
		Logger:           ctrl.Log,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	var eventRecorder *events.Recorder
	if eventRecorder, err = events.NewRecorder(mgr, ctrl.Log, eventsAddr, controllerName); err != nil {
		setupLog.Error(err, "unable to create event recorder")
		os.Exit(1)
	}

	jobStatusReader := statusreaders.NewCustomJobStatusReader(mgr.GetRESTMapper())
	pollingOpts := polling.Options{
		CustomStatusReaders: []engine.StatusReader{jobStatusReader},
	}

	if err = (&controller.KCLRunReconciler{
		ControllerName:          controllerName,
		DefaultServiceAccount:   defaultServiceAccount,
		Client:                  mgr.GetClient(),
		Metrics:                 helper.NewMetrics(mgr, metrics.MustMakeRecorder(), krmkcldevfluxcdv1alpha1.KCLRunFinalizer),
		EventRecorder:           eventRecorder,
		GetClusterConfig:        ctrl.GetConfig,
		ClientOpts:              clientOptions,
		KubeConfigOpts:          kubeConfigOpts,
		PollingOpts:             pollingOpts,
		StatusPoller:            polling.NewStatusPoller(mgr.GetClient(), mgr.GetRESTMapper(), pollingOpts),
		DisallowedFieldManagers: disallowedFieldManagers,
	}).SetupWithManager(ctx, mgr, controller.KCLRunReconcilerOptions{
		DependencyRequeueInterval: requeueDependency,
		HTTPRetry:                 httpRetry,
	}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "KCLRun")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

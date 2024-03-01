/*
Copyright 2014 The Kubernetes Authors.

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

// Package app does all of the work necessary to configure and run a
// Kubernetes app process.
package app

import (
	"context"
	goflag "flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/apiserver/pkg/server/mux"
	"k8s.io/apiserver/pkg/server/routes"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/events"
	cliflag "k8s.io/component-base/cli/flag"
	componentbaseconfig "k8s.io/component-base/config"
	"k8s.io/component-base/configz"
	"k8s.io/component-base/logs"
	logsapi "k8s.io/component-base/logs/api/v1"
	"k8s.io/component-base/metrics"
	metricsfeatures "k8s.io/component-base/metrics/features"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/component-base/metrics/prometheus/slis"
	"k8s.io/component-base/version"
	"k8s.io/component-base/version/verflag"
	nodeutil "k8s.io/component-helpers/node/util"
	"k8s.io/klog/v2"
	"k8s.io/kube-proxy/config/v1alpha1"
	"k8s.io/kube-proxy/config/v1alpha2"
	api "k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/kubernetes/pkg/cluster/ports"
	"k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/kubelet/qos"
	"k8s.io/kubernetes/pkg/proxy"
	"k8s.io/kubernetes/pkg/proxy/apis"
	kubeproxyconfig "k8s.io/kubernetes/pkg/proxy/apis/config"
	proxyconfigscheme "k8s.io/kubernetes/pkg/proxy/apis/config/scheme"
	kubeproxyconfigv1alpha1 "k8s.io/kubernetes/pkg/proxy/apis/config/v1alpha1"
	kubeproxyconfigv1alpha2 "k8s.io/kubernetes/pkg/proxy/apis/config/v1alpha2"
	"k8s.io/kubernetes/pkg/proxy/apis/config/validation"
	"k8s.io/kubernetes/pkg/proxy/config"
	"k8s.io/kubernetes/pkg/proxy/healthcheck"
	proxyutil "k8s.io/kubernetes/pkg/proxy/util"
	"k8s.io/kubernetes/pkg/util/filesystem"
	utilflag "k8s.io/kubernetes/pkg/util/flag"
	utilnode "k8s.io/kubernetes/pkg/util/node"
	"k8s.io/kubernetes/pkg/util/oom"
	netutils "k8s.io/utils/net"
	"k8s.io/utils/ptr"
)

func init() {
	utilruntime.Must(metricsfeatures.AddFeatureGates(utilfeature.DefaultMutableFeatureGate))
	utilruntime.Must(logsapi.AddFeatureGates(utilfeature.DefaultMutableFeatureGate))
}

// proxyRun defines the interface to run a specified ProxyServer
type proxyRun interface {
	Run() error
}

// Options contains everything necessary to create and run a proxy server.
type Options struct {
	// ConfigFile is the location of the proxy server's configuration file.
	ConfigFile string
	// WriteConfigTo is the path where the default configuration will be written.
	WriteConfigTo string
	// CleanupAndExit, when true, makes the proxy server clean up iptables and ipvs rules, then exit.
	CleanupAndExit bool
	// InitAndExit, when true, makes the proxy server makes configurations that need privileged access, then exit.
	InitAndExit bool
	// WindowsService should be set to true if kube-proxy is running as a service on Windows.
	// Its corresponding flag only gets registered in Windows builds
	WindowsService bool
	// config is the proxy server's configuration object.
	config *kubeproxyconfig.KubeProxyConfiguration
	// watcher is used to watch on the update change of ConfigFile
	watcher filesystem.FSWatcher
	// proxyServer is the interface to run the proxy server
	proxyServer proxyRun
	// errCh is the channel that errors will be sent
	errCh chan error

	// The fields below here are placeholders for flags that can't be directly mapped into
	// config.KubeProxyConfiguration.
	//
	// TODO remove these fields once the deprecated flags are removed.

	// master is used to override the kubeconfig's URL to the apiserver.
	master string
	// healthzPort is the port to be used by the healthz server.
	healthzPort int32
	// metricsPort is the port to be used by the metrics server.
	metricsPort int32

	// hostnameOverride, if set from the command line flag, takes precedence over the `HostnameOverride` value from the config file
	hostnameOverride string

	logger klog.Logger

	// The fields below here are placeholders for deprecated flags that can't be directly mapped into
	// config.KubeProxyConfiguration.
	iptablesSyncPeriod    time.Duration
	iptablesMinSyncPeriod time.Duration
	ipvsSyncPeriod        time.Duration
	ipvsMinSyncPeriod     time.Duration
	clusterCIDR           string
	bindAddress           string
	healthzAddress        string
	metricsAddress        string
}

// AddFlags adds flags to fs and binds them to options.
func (o *Options) AddFlags(fs *pflag.FlagSet) {
	o.addOSFlags(fs)

	fs.StringVar(&o.ConfigFile, "config", o.ConfigFile, "The path to the configuration file.")
	fs.StringVar(&o.WriteConfigTo, "write-config-to", o.WriteConfigTo, "If set, write the default configuration values to this file and exit.")

	fs.BoolVar(&o.CleanupAndExit, "cleanup", o.CleanupAndExit, "If true cleanup iptables and ipvs rules and exit.")

	fs.Var(cliflag.NewMapStringBool(&o.config.FeatureGates), "feature-gates", "A set of key=value pairs that describe feature gates for alpha/experimental features. "+
		"Options are:\n"+strings.Join(utilfeature.DefaultFeatureGate.KnownFeatures(), "\n")+"\n"+
		"This parameter is ignored if a config file is specified by --config.")

	fs.StringVar(&o.config.ClientConnection.Kubeconfig, "kubeconfig", o.config.ClientConnection.Kubeconfig, "Path to kubeconfig file with authorization information (the master location can be overridden by the master flag).")
	fs.StringVar(&o.master, "master", o.master, "The address of the Kubernetes API server (overrides any value in kubeconfig)")
	fs.StringVar(&o.config.ClientConnection.ContentType, "kube-api-content-type", o.config.ClientConnection.ContentType, "Content type of requests sent to apiserver.")
	fs.Int32Var(&o.config.ClientConnection.Burst, "kube-api-burst", o.config.ClientConnection.Burst, "Burst to use while talking with kubernetes apiserver")
	fs.Float32Var(&o.config.ClientConnection.QPS, "kube-api-qps", o.config.ClientConnection.QPS, "QPS to use while talking with kubernetes apiserver")

	fs.StringVar(&o.hostnameOverride, "hostname-override", o.hostnameOverride, "If non-empty, will be used as the name of the Node that kube-proxy is running on. If unset, the node name is assumed to be the same as the node's hostname.")
	fs.StringSliceVar(&o.config.NodeIPOverride, "node-ip-override", o.config.NodeIPOverride, "Overrides kube-proxy's idea of what its node's primary IPs are. This parameter is ignored if a config file is specified by --config.")
	fs.Var(&utilflag.IPVar{Val: &o.bindAddress}, "bind-address", "Overrides kube-proxy's idea of what its node's primary IP is. Note that the name is a historical artifact, and kube-proxy does not actually bind any sockets to this IP. This parameter is ignored if a config file is specified by --config.")
	fs.Var(&utilflag.IPPortVar{Val: &o.healthzAddress}, "healthz-bind-address", "The IP address and port for the healthz server to serve on, defaulting to \"0.0.0.0:10256\" (if --bind-address is unset or IPv4), or \"[::]:10256\" (if --bind-address is IPv6). Set empty to disable. This parameter is ignored if a config file is specified by --config.")
	fs.StringSliceVar(&o.config.HealthzBindAddresses, "healthz-bind-addresses", o.config.HealthzBindAddresses, "The is a list of CIDR ranges that contains a valid node IP on which healthz server will be served on. This parameter is ignored if a config file is specified by --config.")
	fs.Int32Var(&o.config.HealthzBindPort, "healthz-bind-port", o.config.HealthzBindPort, "The port for the healthz server to serve on, defaulting to 10256. This parameter is ignored if a config file is specified by --config.")
	fs.Var(&utilflag.IPPortVar{Val: &o.metricsAddress}, "metrics-bind-address", "The IP address and port for the metrics server to serve on, defaulting to \"127.0.0.1:10249\" (if --bind-address is unset or IPv4), or \"[::1]:10249\" (if --bind-address is IPv6). (Set to \"0.0.0.0:10249\" / \"[::]:10249\" to bind on all interfaces.) Set empty to disable. This parameter is ignored if a config file is specified by --config.")
	fs.StringSliceVar(&o.config.MetricsBindAddresses, "metrics-bind-addresses", o.config.MetricsBindAddresses, "The is a list of CIDR ranges that contains a valid node IP on which metrics server will be served on. This parameter is ignored if a config file is specified by --config.")
	fs.Int32Var(&o.config.MetricsBindPort, "metrics-bind-port", o.config.MetricsBindPort, "The port for the metrics server to serve on, defaulting to 10249. This parameter is ignored if a config file is specified by --config.")
	fs.BoolVar(&o.config.BindAddressHardFail, "bind-address-hard-fail", o.config.BindAddressHardFail, "If true kube-proxy will treat failure to bind to a port as fatal and exit")
	fs.BoolVar(&o.config.EnableProfiling, "profiling", o.config.EnableProfiling, "If true enables profiling via web interface on /debug/pprof handler. This parameter is ignored if a config file is specified by --config.")
	fs.StringVar(&o.config.ShowHiddenMetricsForVersion, "show-hidden-metrics-for-version", o.config.ShowHiddenMetricsForVersion,
		"The previous version for which you want to show hidden metrics. "+
			"Only the previous minor version is meaningful, other values will not be allowed. "+
			"The format is <major>.<minor>, e.g.: '1.16'. "+
			"The purpose of this format is make sure you have the opportunity to notice if the next release hides additional metrics, "+
			"rather than being surprised when they are permanently removed in the release after that. "+
			"This parameter is ignored if a config file is specified by --config.")
	fs.BoolVar(&o.InitAndExit, "init-only", o.InitAndExit, "If true, perform any initialization steps that must be done with full root privileges, and then exit. After doing this, you can run kube-proxy again with only the CAP_NET_ADMIN capability.")
	fs.Var(&o.config.Mode, "proxy-mode", "Which proxy mode to use: on Linux this can be 'iptables' (default) or 'ipvs'. On Windows the only supported value is 'kernelspace'."+
		"This parameter is ignored if a config file is specified by --config.")

	fs.DurationVar(&o.config.SyncPeriod.Duration, "sync-period", o.config.SyncPeriod.Duration, "An interval (e.g. '5s', '1m', '2h22m') indicating how frequently various re-synchronizing and cleanup operations are performed. Must be greater than 0.")
	fs.DurationVar(&o.config.MinSyncPeriod.Duration, "min-sync-period", o.config.MinSyncPeriod.Duration, "The minimum period between proxier rule resyncs (e.g. '5s', '1m', '2h22m'). A value of 0 means every Service or EndpointSlice change will result in an immediate proxier resync.")

	fs.BoolVar(&o.config.Linux.MasqueradeAll, "masquerade-all", o.config.Linux.MasqueradeAll, "If using the iptables proxy mode, SNAT all traffic sent via Service cluster IPs. This may be required with some CNI plugins.")

	fs.Int32Var(o.config.IPTables.MasqueradeBit, "iptables-masquerade-bit", ptr.Deref(o.config.IPTables.MasqueradeBit, 14), "If using the iptables proxy mode, the bit of the fwmark space to mark packets requiring SNAT with. Must be within the range [0, 31].")
	fs.BoolVar(o.config.IPTables.LocalhostNodePorts, "iptables-localhost-nodeports", ptr.Deref(o.config.IPTables.LocalhostNodePorts, true), "If false, kube-proxy will disable the legacy behavior of allowing NodePort services to be accessed via localhost. (Applies only to iptables mode and IPv4; localhost NodePorts are never allowed with other proxy modes or with IPv6.)")
	fs.DurationVar(&o.iptablesSyncPeriod, "iptables-sync-period", o.iptablesSyncPeriod, "An interval (e.g. '5s', '1m', '2h22m') indicating how frequently various re-synchronizing and cleanup operations are performed. Must be greater than 0.")
	fs.DurationVar(&o.iptablesMinSyncPeriod, "iptables-min-sync-period", o.iptablesMinSyncPeriod, "The minimum period between iptables rule resyncs (e.g. '5s', '1m', '2h22m'). A value of 0 means every Service or EndpointSlice change will result in an immediate iptables resync.")

	fs.DurationVar(&o.ipvsSyncPeriod, "ipvs-sync-period", o.ipvsSyncPeriod, "An interval (e.g. '5s', '1m', '2h22m') indicating how frequently various re-synchronizing and cleanup operations are performed. Must be greater than 0.")
	fs.DurationVar(&o.ipvsMinSyncPeriod, "ipvs-min-sync-period", o.ipvsMinSyncPeriod, "The minimum period between IPVS rule resyncs (e.g. '5s', '1m', '2h22m'). A value of 0 means every Service or EndpointSlice change will result in an immediate IPVS resync.")
	fs.StringVar(&o.config.IPVS.Scheduler, "ipvs-scheduler", o.config.IPVS.Scheduler, "The ipvs scheduler type when proxy mode is ipvs")
	fs.StringSliceVar(&o.config.IPVS.ExcludeCIDRs, "ipvs-exclude-cidrs", o.config.IPVS.ExcludeCIDRs, "A comma-separated list of CIDRs which the ipvs proxier should not touch when cleaning up IPVS rules.")
	fs.BoolVar(&o.config.IPVS.StrictARP, "ipvs-strict-arp", o.config.IPVS.StrictARP, "Enable strict ARP by setting arp_ignore to 1 and arp_announce to 2")
	fs.DurationVar(&o.config.IPVS.TCPTimeout.Duration, "ipvs-tcp-timeout", o.config.IPVS.TCPTimeout.Duration, "The timeout for idle IPVS TCP connections, 0 to leave as-is. (e.g. '5s', '1m', '2h22m').")
	fs.DurationVar(&o.config.IPVS.TCPFinTimeout.Duration, "ipvs-tcpfin-timeout", o.config.IPVS.TCPFinTimeout.Duration, "The timeout for IPVS TCP connections after receiving a FIN packet, 0 to leave as-is. (e.g. '5s', '1m', '2h22m').")
	fs.DurationVar(&o.config.IPVS.UDPTimeout.Duration, "ipvs-udp-timeout", o.config.IPVS.UDPTimeout.Duration, "The timeout for IPVS UDP packets, 0 to leave as-is. (e.g. '5s', '1m', '2h22m').")
	fs.Int32Var(o.config.IPVS.MasqueradeBit, "ipvs-masquerade-bit", ptr.Deref(o.config.IPVS.MasqueradeBit, 14), "If using the ipvs proxy mode, the bit of the fwmark space to mark packets requiring SNAT with.  Must be within the range [0, 31].")

	fs.Var(&o.config.DetectLocalMode, "detect-local-mode", "Mode to use to detect local traffic. This parameter is ignored if a config file is specified by --config.")
	fs.StringVar(&o.config.DetectLocal.BridgeInterface, "pod-bridge-interface", o.config.DetectLocal.BridgeInterface, "A bridge interface name. When --detect-local-mode is set to BridgeInterface, kube-proxy will consider traffic to be local if it originates from this bridge.")
	fs.StringVar(&o.config.DetectLocal.InterfaceNamePrefix, "pod-interface-name-prefix", o.config.DetectLocal.InterfaceNamePrefix, "An interface name prefix. When --detect-local-mode is set to InterfaceNamePrefix, kube-proxy will consider traffic to be local if it originates from any interface whose name begins with this prefix.")
	fs.StringSliceVar(&o.config.DetectLocal.ClusterCIDRs, "cluster-cidrs", o.config.DetectLocal.ClusterCIDRs, "The CIDR range of the pods in the cluster. When --detect-local-mode is set to ClusterCIDR, kube-proxy will consider traffic to be local if its source IP is in this range. (Otherwise it is not used.) "+
		"This parameter is ignored if a config file is specified by --config.")
	fs.StringVar(&o.clusterCIDR, "cluster-cidr", o.clusterCIDR, "The CIDR range of the pods in the cluster. (For dual-stack clusters, this can be a comma-separated dual-stack pair of CIDR ranges.). When --detect-local-mode is set to ClusterCIDR, kube-proxy will consider traffic to be local if its source IP is in this range. (Otherwise it is not used.) "+
		"This parameter is ignored if a config file is specified by --config.")

	fs.StringSliceVar(&o.config.NodePortAddresses, "nodeport-addresses", o.config.NodePortAddresses,
		"A list of CIDR ranges that contain valid node IPs. If set, connections to NodePort services will only be accepted on node IPs in one of the indicated ranges. If unset, NodePort connections will be accepted on all local IPs. This parameter is ignored if a config file is specified by --config.")

	fs.Int32Var(o.config.Linux.OOMScoreAdj, "oom-score-adj", ptr.Deref(o.config.Linux.OOMScoreAdj, int32(qos.KubeProxyOOMScoreAdj)), "The oom-score-adj value for kube-proxy process. Values must be within the range [-1000, 1000]. This parameter is ignored if a config file is specified by --config.")
	fs.Int32Var(o.config.Linux.Conntrack.MaxPerCore, "conntrack-max-per-core", *o.config.Linux.Conntrack.MaxPerCore,
		"Maximum number of NAT connections to track per CPU core (0 to leave the limit as-is and ignore conntrack-min).")
	fs.Int32Var(o.config.Linux.Conntrack.Min, "conntrack-min", *o.config.Linux.Conntrack.Min,
		"Minimum number of conntrack entries to allocate, regardless of conntrack-max-per-core (set conntrack-max-per-core=0 to leave the limit as-is).")

	fs.DurationVar(&o.config.Linux.Conntrack.TCPEstablishedTimeout.Duration, "conntrack-tcp-timeout-established", o.config.Linux.Conntrack.TCPEstablishedTimeout.Duration, "Idle timeout for established TCP connections (0 to leave as-is)")
	fs.DurationVar(
		&o.config.Linux.Conntrack.TCPCloseWaitTimeout.Duration, "conntrack-tcp-timeout-close-wait",
		o.config.Linux.Conntrack.TCPCloseWaitTimeout.Duration,
		"NAT timeout for TCP connections in the CLOSE_WAIT state")
	fs.BoolVar(&o.config.Linux.Conntrack.TCPBeLiberal, "conntrack-tcp-be-liberal", o.config.Linux.Conntrack.TCPBeLiberal, "Enable liberal mode for tracking TCP packets by setting nf_conntrack_tcp_be_liberal to 1")
	fs.DurationVar(&o.config.Linux.Conntrack.UDPTimeout.Duration, "conntrack-udp-timeout", o.config.Linux.Conntrack.UDPTimeout.Duration, "Idle timeout for UNREPLIED UDP connections (0 to leave as-is)")
	fs.DurationVar(&o.config.Linux.Conntrack.UDPStreamTimeout.Duration, "conntrack-udp-timeout-stream", o.config.Linux.Conntrack.UDPStreamTimeout.Duration, "Idle timeout for ASSURED UDP connections (0 to leave as-is)")

	fs.DurationVar(&o.config.ConfigSyncPeriod.Duration, "config-sync-period", o.config.ConfigSyncPeriod.Duration, "How often configuration from the apiserver is refreshed.  Must be greater than 0.")

	_ = fs.MarkDeprecated("iptables-sync-period", "This flag is deprecated and will be removed in a future release. Please use --sync-period instead.")
	_ = fs.MarkDeprecated("ipvs-sync-period", "This flag is deprecated and will be removed in a future release. Please use --sync-period instead.")
	_ = fs.MarkDeprecated("iptables-min-sync-period", "This flag is deprecated and will be removed in a future release. Please use --min-sync-period instead.")
	_ = fs.MarkDeprecated("ipvs-min-sync-period", "This flag is deprecated and will be removed in a future release. Please use --min-sync-period instead.")
	_ = fs.MarkDeprecated("cluster-cidr", "This flag is deprecated and will be removed in a future release. Please use --cluster-cidrs instead.")
	_ = fs.MarkDeprecated("bind-address", "This flag is deprecated and will be removed in a future release. Please use --node-ip-override instead.")
	_ = fs.MarkDeprecated("healthz-bind-address", "This flag is deprecated and will be removed in a future release. Please use --healthz-bind-addresses and --healthz-bind-port instead.")
	_ = fs.MarkDeprecated("metrics-bind-address", "This flag is deprecated and will be removed in a future release. Please use --metrics-bind-addresses and --metrics-bind-port instead.")

	logsapi.AddFlags(&o.config.Logging, fs)
}

// newKubeProxyConfiguration returns a KubeProxyConfiguration with default values
func newKubeProxyConfiguration() *kubeproxyconfig.KubeProxyConfiguration {
	versionedConfig := &v1alpha1.KubeProxyConfiguration{}
	proxyconfigscheme.Scheme.Default(versionedConfig)
	internalConfig, err := proxyconfigscheme.Scheme.ConvertToVersion(versionedConfig, kubeproxyconfig.SchemeGroupVersion)
	if err != nil {
		panic(fmt.Sprintf("Unable to create default config: %v", err))
	}

	return internalConfig.(*kubeproxyconfig.KubeProxyConfiguration)
}

// NewOptions returns initialized Options
func NewOptions() *Options {
	return &Options{
		config:      newKubeProxyConfiguration(),
		healthzPort: ports.ProxyHealthzPort,
		metricsPort: ports.ProxyStatusPort,
		errCh:       make(chan error),
		logger:      klog.FromContext(context.Background()),
	}
}

// Complete completes all the required options.
func (o *Options) Complete(fs *pflag.FlagSet) error {
	// Load the config file here in Complete, so that Validate validates the fully-resolved config.
	if len(o.ConfigFile) > 0 {
		c, err := o.loadConfigFromFile(o.ConfigFile)
		if err != nil {
			return err
		}

		// Before we overwrite the config which holds the parsed
		// command line parameters, we need to copy all modified
		// logging settings over to the loaded config (i.e.  logging
		// command line flags have priority). Otherwise `--config
		// ... -v=5` doesn't work (config resets verbosity even
		// when it contains no logging settings).
		copyLogsFromFlags(fs, &c.Logging)
		o.config = c

		if err := o.initWatcher(); err != nil {
			return err
		}
	} else {
		// process the deprecated flags if config file is not passed.
		o.processDeprecatedFlags(fs)
	}

	o.platformApplyDefaults(o.config)

	if err := o.processHostnameOverrideFlag(); err != nil {
		return err
	}

	return utilfeature.DefaultMutableFeatureGate.SetFromMap(o.config.FeatureGates)
}

// copyLogsFromFlags applies the logging flags from the given flag set to the given
// configuration. Fields for which the corresponding flag was not used are left
// unmodified. For fields that have multiple values (like vmodule), the values from
// the flags get joined so that the command line flags have priority.
//
// TODO (pohly): move this to logsapi
func copyLogsFromFlags(from *pflag.FlagSet, to *logsapi.LoggingConfiguration) error {
	var cloneFS pflag.FlagSet
	logsapi.AddFlags(to, &cloneFS)
	vmodule := to.VModule
	to.VModule = nil
	var err error
	cloneFS.VisitAll(func(f *pflag.Flag) {
		if err != nil {
			return
		}
		fsFlag := from.Lookup(f.Name)
		if fsFlag == nil {
			err = fmt.Errorf("logging flag %s not found in flag set", f.Name)
			return
		}
		if !fsFlag.Changed {
			return
		}
		if setErr := f.Value.Set(fsFlag.Value.String()); setErr != nil {
			err = fmt.Errorf("copying flag %s value: %v", f.Name, setErr)
			return
		}
	})
	to.VModule = append(to.VModule, vmodule...)
	return err
}

// Creates a new filesystem watcher and adds watches for the config file.
func (o *Options) initWatcher() error {
	fswatcher := filesystem.NewFsnotifyWatcher()
	err := fswatcher.Init(o.eventHandler, o.errorHandler)
	if err != nil {
		return err
	}
	err = fswatcher.AddWatch(o.ConfigFile)
	if err != nil {
		return err
	}
	o.watcher = fswatcher
	return nil
}

func (o *Options) eventHandler(ent fsnotify.Event) {
	if ent.Has(fsnotify.Write) || ent.Has(fsnotify.Rename) {
		// error out when ConfigFile is updated
		o.errCh <- fmt.Errorf("content of the proxy server's configuration file was updated")
		return
	}
	o.errCh <- nil
}

func (o *Options) errorHandler(err error) {
	o.errCh <- err
}

// processHostnameOverrideFlag processes hostname-override flag
func (o *Options) processHostnameOverrideFlag() error {
	// Check if hostname-override flag is set and use value since configFile always overrides
	if len(o.hostnameOverride) > 0 {
		hostName := strings.TrimSpace(o.hostnameOverride)
		if len(hostName) == 0 {
			return fmt.Errorf("empty hostname-override is invalid")
		}
		o.config.HostnameOverride = strings.ToLower(hostName)
	}

	return nil
}

// processDeprecatedFlags processes deprecated flags which can't be directly mapped to internal config.
func (o *Options) processDeprecatedFlags(fs *pflag.FlagSet) {
	visited := make(map[string]bool)
	fs.Visit(func(f *pflag.Flag) {
		visited[f.Name] = true
	})

	fs.Visit(func(f *pflag.Flag) {
		switch {
		case f.Name == "iptables-sync-period" && !visited["sync-period"] && o.config.Mode == kubeproxyconfig.ProxyModeIPTables:
			o.config.SyncPeriod.Duration = o.iptablesSyncPeriod
		case f.Name == "iptables-min-sync-period" && !visited["sync-period"] && o.config.Mode == kubeproxyconfig.ProxyModeIPTables:
			o.config.SyncPeriod.Duration = o.iptablesSyncPeriod
		case f.Name == "ipvs-sync-period" && !visited["sync-period"] && o.config.Mode == kubeproxyconfig.ProxyModeIPVS:
			o.config.SyncPeriod.Duration = o.ipvsSyncPeriod
		case f.Name == "ipvs-min-sync-period" && !visited["sync-period"] && o.config.Mode == kubeproxyconfig.ProxyModeIPVS:
			o.config.SyncPeriod.Duration = o.ipvsMinSyncPeriod
		case f.Name == "cluster-cidr" && !visited["cluster-cidrs"]:
			o.config.DetectLocal.ClusterCIDRs = strings.Split(o.clusterCIDR, ",")
		case f.Name == "bind-address" && !visited["node-ip-override"]:
			o.config.NodeIPOverride = []string{o.bindAddress}
		case f.Name == "healthz-bind-address":
			host, port, _ := net.SplitHostPort(o.healthzAddress)
			if !visited["healthz-bind-addresses"] {
				ip := netutils.ParseIPSloppy(host)
				if ip.IsUnspecified() {
					o.config.HealthzBindAddresses = []string{fmt.Sprintf("%s/0", host)}
				} else if netutils.IsIPv4(ip) {
					o.config.HealthzBindAddresses = []string{fmt.Sprintf("%s/32", host)}
				} else {
					o.config.HealthzBindAddresses = []string{fmt.Sprintf("%s/128", host)}
				}
			}
			if !visited["healthz-bind-port"] {
				intPort, _ := strconv.Atoi(port)
				o.config.HealthzBindPort = int32(intPort)
			}
		case f.Name == "metrics-bind-address":
			host, port, _ := net.SplitHostPort(o.healthzAddress)
			if !visited["metrics-bind-addresses"] {
				ip := netutils.ParseIPSloppy(host)
				if ip.IsUnspecified() {
					o.config.MetricsBindAddresses = []string{fmt.Sprintf("%s/0", host)}
				} else if netutils.IsIPv4(ip) {
					o.config.MetricsBindAddresses = []string{fmt.Sprintf("%s/32", host)}
				} else {
					o.config.MetricsBindAddresses = []string{fmt.Sprintf("%s/128", host)}
				}
			}
			if !visited["metrics-bind-port"] {
				intPort, _ := strconv.Atoi(port)
				o.config.MetricsBindPort = int32(intPort)
			}
		}
	})
}

// Validate validates all the required options.
func (o *Options) Validate() error {
	if errs := validation.Validate(o.config); len(errs) != 0 {
		return errs.ToAggregate()
	}

	return nil
}

// Run runs the specified ProxyServer.
func (o *Options) Run() error {
	defer close(o.errCh)
	if len(o.WriteConfigTo) > 0 {
		return o.writeConfigFile()
	}

	err := platformCleanup(o.config.Mode, o.CleanupAndExit)
	if o.CleanupAndExit {
		return err
	}
	// We ignore err otherwise; the cleanup is best-effort, and the backends will have
	// logged messages if they failed in interesting ways.

	proxyServer, err := newProxyServer(o.logger, o.config, o.master, o.InitAndExit)
	if err != nil {
		return err
	}
	if o.InitAndExit {
		return nil
	}

	o.proxyServer = proxyServer
	return o.runLoop()
}

// runLoop will watch on the update change of the proxy server's configuration file.
// Return an error when updated
func (o *Options) runLoop() error {
	if o.watcher != nil {
		o.watcher.Run()
	}

	// run the proxy in goroutine
	go func() {
		err := o.proxyServer.Run()
		o.errCh <- err
	}()

	for {
		err := <-o.errCh
		if err != nil {
			return err
		}
	}
}

func (o *Options) writeConfigFile() (err error) {
	const mediaType = runtime.ContentTypeYAML
	info, ok := runtime.SerializerInfoForMediaType(proxyconfigscheme.Codecs.SupportedMediaTypes(), mediaType)
	if !ok {
		return fmt.Errorf("unable to locate encoder -- %q is not a supported media type", mediaType)
	}

	encoder := proxyconfigscheme.Codecs.EncoderForVersion(info.Serializer, v1alpha2.SchemeGroupVersion)

	configFile, err := os.Create(o.WriteConfigTo)
	if err != nil {
		return err
	}

	defer func() {
		ferr := configFile.Close()
		if ferr != nil && err == nil {
			err = ferr
		}
	}()

	if err = encoder.Encode(o.config, configFile); err != nil {
		return err
	}

	o.logger.Info("Wrote configuration", "file", o.WriteConfigTo)

	return nil
}

// addressFromDeprecatedFlags returns server address from flags
// passed on the command line based on the following rules:
// 1. If port is 0, disable the server (e.g. set address to empty).
// 2. Otherwise, set the port portion of the config accordingly.
func addressFromDeprecatedFlags(addr string, port int32) string {
	if port == 0 {
		return ""
	}
	return proxyutil.AppendPortIfNeeded(addr, port)
}

// newLenientSchemeAndCodecs returns a scheme that has only v1alpha1 registered into
// it and a CodecFactory with strict decoding disabled.
func newLenientSchemeAndCodecs() (*runtime.Scheme, *serializer.CodecFactory, error) {
	lenientScheme := runtime.NewScheme()
	if err := kubeproxyconfig.AddToScheme(lenientScheme); err != nil {
		return nil, nil, fmt.Errorf("failed to add kube-proxy config API to lenient scheme: %v", err)
	}
	if err := kubeproxyconfigv1alpha1.AddToScheme(lenientScheme); err != nil {
		return nil, nil, fmt.Errorf("failed to add kube-proxy config v1alpha1 API to lenient scheme: %v", err)
	}
	if err := kubeproxyconfigv1alpha2.AddToScheme(lenientScheme); err != nil {
		return nil, nil, fmt.Errorf("failed to add kube-proxy config v1alpha2 API to lenient scheme: %v", err)
	}
	lenientCodecs := serializer.NewCodecFactory(lenientScheme, serializer.DisableStrict)
	return lenientScheme, &lenientCodecs, nil
}

// loadConfigFromFile loads the contents of file and decodes it as a
// KubeProxyConfiguration object.
func (o *Options) loadConfigFromFile(file string) (*kubeproxyconfig.KubeProxyConfiguration, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	return o.loadConfig(data)
}

// loadConfig decodes a serialized KubeProxyConfiguration to the internal type.
func (o *Options) loadConfig(data []byte) (*kubeproxyconfig.KubeProxyConfiguration, error) {

	configObj, gvk, err := proxyconfigscheme.Codecs.UniversalDecoder().Decode(data, nil, nil)
	if err != nil {
		// Try strict decoding first. If that fails decode with a lenient
		// decoder, which has only v1alpha1 registered, and log a warning.
		// The lenient path is to be dropped when support for v1alpha1 is dropped.
		if !runtime.IsStrictDecodingError(err) {
			return nil, fmt.Errorf("failed to decode: %w", err)
		}

		_, lenientCodecs, lenientErr := newLenientSchemeAndCodecs()
		if lenientErr != nil {
			return nil, lenientErr
		}

		configObj, gvk, lenientErr = lenientCodecs.UniversalDecoder().Decode(data, nil, nil)
		if lenientErr != nil {
			// Lenient decoding failed with the current version, return the
			// original strict error.
			return nil, fmt.Errorf("failed lenient decoding: %v", err)
		}

		// Continue with the v1alpha1 object that was decoded leniently, but emit a warning.
		o.logger.Info("Using lenient decoding as strict decoding failed", "err", err)
	}

	proxyConfig, ok := configObj.(*kubeproxyconfig.KubeProxyConfiguration)
	if !ok {
		return nil, fmt.Errorf("got unexpected config type: %v", gvk)
	}
	return proxyConfig, nil
}

// NewProxyCommand creates a *cobra.Command object with default parameters
func NewProxyCommand() *cobra.Command {
	opts := NewOptions()

	cmd := &cobra.Command{
		Use: "kube-proxy",
		Long: `The Kubernetes network proxy runs on each node. This
reflects services as defined in the Kubernetes API on each node and can do simple
TCP, UDP, and SCTP stream forwarding or round robin TCP, UDP, and SCTP forwarding across a set of backends.
Service cluster IPs and ports are currently found through Docker-links-compatible
environment variables specifying ports opened by the service proxy. There is an optional
addon that provides cluster DNS for these cluster IPs. The user must create a service
with the apiserver API to configure the proxy.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			verflag.PrintAndExitIfRequested()

			if err := initForOS(opts.WindowsService); err != nil {
				return fmt.Errorf("failed os init: %w", err)
			}

			if err := opts.Complete(cmd.Flags()); err != nil {
				return fmt.Errorf("failed complete: %w", err)
			}

			logs.InitLogs()
			if err := logsapi.ValidateAndApplyAsField(&opts.config.Logging, utilfeature.DefaultFeatureGate, field.NewPath("logging")); err != nil {
				return fmt.Errorf("initialize logging: %v", err)
			}

			cliflag.PrintFlags(cmd.Flags())

			if err := opts.Validate(); err != nil {
				return fmt.Errorf("failed validate: %w", err)
			}
			// add feature enablement metrics
			utilfeature.DefaultMutableFeatureGate.AddMetrics()
			if err := opts.Run(); err != nil {
				opts.logger.Error(err, "Error running ProxyServer")
				return err
			}

			return nil
		},
		Args: func(cmd *cobra.Command, args []string) error {
			for _, arg := range args {
				if len(arg) > 0 {
					return fmt.Errorf("%q does not take any arguments, got %q", cmd.CommandPath(), args)
				}
			}
			return nil
		},
	}

	fs := cmd.Flags()
	opts.AddFlags(fs)
	fs.AddGoFlagSet(goflag.CommandLine) // for --boot-id-file and --machine-id-file

	_ = cmd.MarkFlagFilename("config", "yaml", "yml", "json")

	return cmd
}

// ProxyServer represents all the parameters required to start the Kubernetes proxy server. All
// fields are required.
type ProxyServer struct {
	Config *kubeproxyconfig.KubeProxyConfiguration

	Client          clientset.Interface
	Broadcaster     events.EventBroadcaster
	Recorder        events.EventRecorder
	NodeRef         *v1.ObjectReference
	HealthzServer   *healthcheck.ProxierHealthServer
	Hostname        string
	PrimaryIPFamily v1.IPFamily
	NodeIPs         map[v1.IPFamily]net.IP

	podCIDRs []string // only used for LocalModeNodeCIDR

	Proxier proxy.Provider

	logger klog.Logger
}

// newProxyServer creates a ProxyServer based on the given config
func newProxyServer(logger klog.Logger, config *kubeproxyconfig.KubeProxyConfiguration, master string, initOnly bool) (*ProxyServer, error) {
	s := &ProxyServer{
		Config: config,
		logger: logger,
	}

	cz, err := configz.New(kubeproxyconfig.GroupName)
	if err != nil {
		return nil, fmt.Errorf("unable to register configz: %s", err)
	}
	cz.Set(config)

	if len(config.ShowHiddenMetricsForVersion) > 0 {
		metrics.SetShowHidden()
	}

	s.Hostname, err = nodeutil.GetHostname(config.HostnameOverride)
	if err != nil {
		return nil, err
	}

	s.Client, err = createClient(logger, config.ClientConnection, master)
	if err != nil {
		return nil, err
	}

	rawNodeIPs := getNodeIPs(logger, s.Client, s.Hostname)
	s.PrimaryIPFamily, s.NodeIPs = detectNodeIPs(logger, rawNodeIPs, config.NodeIPOverride)

	s.Broadcaster = events.NewBroadcaster(&events.EventSinkImpl{Interface: s.Client.EventsV1()})
	s.Recorder = s.Broadcaster.NewRecorder(proxyconfigscheme.Scheme, "kube-proxy")

	s.NodeRef = &v1.ObjectReference{
		Kind:      "Node",
		Name:      s.Hostname,
		UID:       types.UID(s.Hostname),
		Namespace: "",
	}

	if len(config.HealthzBindAddresses) > 0 {
		s.HealthzServer = healthcheck.NewProxierHealthServer(config.HealthzBindAddresses, config.HealthzBindPort, 2*config.SyncPeriod.Duration)
	}

	err = s.platformSetup()
	if err != nil {
		return nil, err
	}

	ipv4Supported, ipv6Supported, dualStackSupported, err := s.platformCheckSupported()
	if err != nil {
		return nil, err
	} else if (s.PrimaryIPFamily == v1.IPv4Protocol && !ipv4Supported) || (s.PrimaryIPFamily == v1.IPv6Protocol && !ipv6Supported) {
		return nil, fmt.Errorf("no support for primary IP family %q", s.PrimaryIPFamily)
	} else if dualStackSupported {
		logger.Info("kube-proxy running in dual-stack mode", "primary ipFamily", s.PrimaryIPFamily)
	} else {
		logger.Info("kube-proxy running in single-stack mode", "ipFamily", s.PrimaryIPFamily)
	}

	err, fatal := checkIPConfig(s, dualStackSupported)
	if err != nil {
		if fatal || (config.ConfigHardFail != nil && *config.ConfigHardFail) {
			return nil, fmt.Errorf("kube-proxy configuration is incorrect: %v", err)
		}
		logger.Error(err, "Kube-proxy configuration may be incomplete or incorrect")
	}

	s.Proxier, err = s.createProxier(config, dualStackSupported, initOnly)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// checkIPConfig confirms that s has proper configuration for its primary IP family.
func checkIPConfig(s *ProxyServer, dualStackSupported bool) (error, bool) {
	var errors []error
	var badFamily netutils.IPFamily

	if s.PrimaryIPFamily == v1.IPv4Protocol {
		badFamily = netutils.IPv6
	} else {
		badFamily = netutils.IPv4
	}

	var clusterType string
	if dualStackSupported {
		clusterType = fmt.Sprintf("%s-primary", s.PrimaryIPFamily)
	} else {
		clusterType = fmt.Sprintf("%s-only", s.PrimaryIPFamily)
	}

	// Historically, we did not check most of the config options, so we cannot
	// retroactively make IP family mismatches in those options be fatal. When we add
	// new options to check here, we should make problems with those options be fatal.
	fatal := false

	if len(s.Config.DetectLocal.ClusterCIDRs) > 0 {
		if badCIDRs(s.Config.DetectLocal.ClusterCIDRs, badFamily) {
			errors = append(errors, fmt.Errorf("cluster is %s but clusterCIDRs contains only IPv%s addresses", clusterType, badFamily))
			if s.Config.DetectLocalMode == kubeproxyconfig.LocalModeClusterCIDR && !dualStackSupported {
				// This has always been a fatal error
				fatal = true
			}
		}
	}

	if badCIDRs(s.Config.NodePortAddresses, badFamily) {
		errors = append(errors, fmt.Errorf("cluster is %s but nodePortAddresses contains only IPv%s addresses", clusterType, badFamily))
	}

	if badCIDRs(s.podCIDRs, badFamily) {
		errors = append(errors, fmt.Errorf("cluster is %s but node.spec.podCIDRs contains only IPv%s addresses", clusterType, badFamily))
		if s.Config.DetectLocalMode == kubeproxyconfig.LocalModeNodeCIDR {
			// This has always been a fatal error
			fatal = true
		}
	}

	if netutils.IPFamilyOfString(s.Config.Winkernel.SourceVip) == badFamily {
		errors = append(errors, fmt.Errorf("cluster is %s but winkernel.sourceVip is IPv%s", clusterType, badFamily))
	}

	// In some cases, wrong-IP-family is only a problem when the secondary IP family
	// isn't present at all.
	if !dualStackSupported {
		if badCIDRs(s.Config.IPVS.ExcludeCIDRs, badFamily) {
			errors = append(errors, fmt.Errorf("cluster is %s but ipvs.excludeCIDRs contains only IPv%s addresses", clusterType, badFamily))
		}

		if badCIDRs(s.Config.HealthzBindAddresses, badFamily) {
			errors = append(errors, fmt.Errorf("cluster is %s but healthzBindAddresses contains only IPv%s addresses", clusterType, badFamily))
		}
		if badCIDRs(s.Config.MetricsBindAddresses, badFamily) {
			errors = append(errors, fmt.Errorf("cluster is %s but metricsBindAddresses contains only IPv%s addresses", clusterType, badFamily))
		}
	}

	return utilerrors.NewAggregate(errors), fatal
}

// badCIDRs returns true if cidrs is a non-empty list of CIDRs, all of wrongFamily or contains an unspecified cidr.
func badCIDRs(cidrs []string, wrongFamily netutils.IPFamily) bool {
	if len(cidrs) == 0 {
		return false
	}
	for _, cidr := range cidrs {
		ip, _, _ := netutils.ParseCIDRSloppy(cidr)
		if ip.IsUnspecified() || netutils.IPFamilyOfCIDRString(cidr) != wrongFamily {
			return false
		}
	}
	return true
}

// createClient creates a kube client from the given config and masterOverride.
// TODO remove masterOverride when CLI flags are removed.
func createClient(logger klog.Logger, config componentbaseconfig.ClientConnectionConfiguration, masterOverride string) (clientset.Interface, error) {
	var kubeConfig *rest.Config
	var err error

	if len(config.Kubeconfig) == 0 && len(masterOverride) == 0 {
		logger.Info("Neither kubeconfig file nor master URL was specified, falling back to in-cluster config")
		kubeConfig, err = rest.InClusterConfig()
	} else {
		// This creates a client, first loading any specified kubeconfig
		// file, and then overriding the Master flag, if non-empty.
		kubeConfig, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: config.Kubeconfig},
			&clientcmd.ConfigOverrides{ClusterInfo: clientcmdapi.Cluster{Server: masterOverride}}).ClientConfig()
	}
	if err != nil {
		return nil, err
	}

	kubeConfig.AcceptContentTypes = config.AcceptContentTypes
	kubeConfig.ContentType = config.ContentType
	kubeConfig.QPS = config.QPS
	kubeConfig.Burst = int(config.Burst)

	client, err := clientset.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func serveMetrics(logger klog.Logger, cidrStrings []string, port int32, proxyMode kubeproxyconfig.ProxyMode, enableProfiling bool, bindHardFail bool) chan error {
	proxyMux := mux.NewPathRecorderMux("kube-proxy")
	healthz.InstallHandler(proxyMux)
	slis.SLIMetricsWithReset{}.Install(proxyMux)

	proxyMux.HandleFunc("/proxyMode", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		fmt.Fprintf(w, "%s", proxyMode)
	})

	proxyMux.Handle("/metrics", legacyregistry.Handler())

	if enableProfiling {
		routes.Profiling{}.Install(proxyMux)
		routes.DebugFlags{}.Install(proxyMux, "v", routes.StringFlagPutHandler(logs.GlogSetter))
	}

	configz.InstallHandler(proxyMux)

	var nodeIPs []net.IP
	for _, family := range []v1.IPFamily{v1.IPv4Protocol, v1.IPv6Protocol} {
		npa := proxyutil.NewNodePortAddresses(family, cidrStrings, nil)
		ips, err := npa.GetNodeIPs(proxyutil.RealNetwork{})
		if err != nil {
			logger.Error(err, "failed to get get node ips for metrics server")
		}
		nodeIPs = append(nodeIPs, ips...)
	}
	if len(nodeIPs) == 0 {
		logger.Info("failed to get any node ip matching metricsBindAddresses", "metricsBindAddresses", cidrStrings)
	}

	errCh := make(chan error)
	for _, nodeIP := range nodeIPs {
		stopCh := make(chan struct{})
		addr := net.JoinHostPort(nodeIP.String(), strconv.Itoa(int(port)))
		fn := func() {
			err := http.ListenAndServe(addr, proxyMux)
			if err != nil {
				err = fmt.Errorf("starting metrics server failed: %v", err)
				utilruntime.HandleError(err)
				if err != nil && bindHardFail {
					stopCh <- struct{}{}
					errCh <- err
				}
			}
		}
		go wait.Until(fn, 5*time.Second, stopCh)
	}
	return errCh
}

// Run runs the specified ProxyServer. This should never exit (unless CleanupAndExit is set).
// TODO: At the moment, Run() cannot return a nil error, otherwise it's caller will never exit. Update callers of Run to handle nil errors.
func (s *ProxyServer) Run() error {
	// To help debugging, immediately log version
	s.logger.Info("Version info", "version", version.Get())

	s.logger.Info("Golang settings", "GOGC", os.Getenv("GOGC"), "GOMAXPROCS", os.Getenv("GOMAXPROCS"), "GOTRACEBACK", os.Getenv("GOTRACEBACK"))

	// TODO(vmarmol): Use container config for this.
	var oomAdjuster *oom.OOMAdjuster
	if s.Config.Linux.OOMScoreAdj != nil {
		oomAdjuster = oom.NewOOMAdjuster()
		if err := oomAdjuster.ApplyOOMScoreAdj(0, int(*s.Config.Linux.OOMScoreAdj)); err != nil {
			s.logger.V(2).Info("Failed to apply OOMScore", "err", err)
		}
	}

	if s.Broadcaster != nil {
		stopCh := make(chan struct{})
		s.Broadcaster.StartRecordingToSink(stopCh)
	}

	// TODO(thockin): make it possible for healthz and metrics to be on the same port.
	// Start up a healthz server if requested
	var healthzErrCh chan error
	if s.HealthzServer != nil {
		healthzErrCh = s.HealthzServer.Run(s.Config.BindAddressHardFail)
	}

	// Start up a metrics server if requested
	var metricsErrCh chan error
	if len(s.Config.MetricsBindAddresses) > 0 {
		metricsErrCh = serveMetrics(s.logger, s.Config.MetricsBindAddresses, s.Config.MetricsBindPort, s.Config.Mode, s.Config.EnableProfiling, s.Config.BindAddressHardFail)
	}

	noProxyName, err := labels.NewRequirement(apis.LabelServiceProxyName, selection.DoesNotExist, nil)
	if err != nil {
		return err
	}

	noHeadlessEndpoints, err := labels.NewRequirement(v1.IsHeadlessService, selection.DoesNotExist, nil)
	if err != nil {
		return err
	}

	labelSelector := labels.NewSelector()
	labelSelector = labelSelector.Add(*noProxyName, *noHeadlessEndpoints)

	// Make informers that filter out objects that want a non-default service proxy.
	informerFactory := informers.NewSharedInformerFactoryWithOptions(s.Client, s.Config.ConfigSyncPeriod.Duration,
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = labelSelector.String()
		}))

	// Create configs (i.e. Watches for Services, EndpointSlices and ServiceCIDRs)
	// Note: RegisterHandler() calls need to happen before creation of Sources because sources
	// only notify on changes, and the initial update (on process start) may be lost if no handlers
	// are registered yet.
	serviceConfig := config.NewServiceConfig(informerFactory.Core().V1().Services(), s.Config.ConfigSyncPeriod.Duration)
	serviceConfig.RegisterEventHandler(s.Proxier)
	go serviceConfig.Run(wait.NeverStop)

	endpointSliceConfig := config.NewEndpointSliceConfig(informerFactory.Discovery().V1().EndpointSlices(), s.Config.ConfigSyncPeriod.Duration)
	endpointSliceConfig.RegisterEventHandler(s.Proxier)
	go endpointSliceConfig.Run(wait.NeverStop)

	if utilfeature.DefaultFeatureGate.Enabled(features.MultiCIDRServiceAllocator) {
		serviceCIDRConfig := config.NewServiceCIDRConfig(informerFactory.Networking().V1alpha1().ServiceCIDRs(), s.Config.ConfigSyncPeriod.Duration)
		serviceCIDRConfig.RegisterEventHandler(s.Proxier)
		go serviceCIDRConfig.Run(wait.NeverStop)
	}
	// This has to start after the calls to NewServiceConfig because that
	// function must configure its shared informer event handlers first.
	informerFactory.Start(wait.NeverStop)

	// Make an informer that selects for our nodename.
	currentNodeInformerFactory := informers.NewSharedInformerFactoryWithOptions(s.Client, s.Config.ConfigSyncPeriod.Duration,
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = fields.OneTermEqualSelector("metadata.name", s.NodeRef.Name).String()
		}))
	nodeConfig := config.NewNodeConfig(currentNodeInformerFactory.Core().V1().Nodes(), s.Config.ConfigSyncPeriod.Duration)
	// https://issues.k8s.io/111321
	if s.Config.DetectLocalMode == kubeproxyconfig.LocalModeNodeCIDR {
		nodeConfig.RegisterEventHandler(proxy.NewNodePodCIDRHandler(s.podCIDRs))
	}
	if utilfeature.DefaultFeatureGate.Enabled(features.KubeProxyDrainingTerminatingNodes) {
		nodeConfig.RegisterEventHandler(&proxy.NodeEligibleHandler{
			HealthServer: s.HealthzServer,
		})
	}
	nodeConfig.RegisterEventHandler(s.Proxier)

	go nodeConfig.Run(wait.NeverStop)

	// This has to start after the calls to NewNodeConfig because that must
	// configure the shared informer event handler first.
	currentNodeInformerFactory.Start(wait.NeverStop)

	// Birth Cry after the birth is successful
	s.birthCry()

	go s.Proxier.SyncLoop()

	select {
	case err = <-healthzErrCh:
		s.Recorder.Eventf(s.NodeRef, nil, api.EventTypeWarning, "FailedToStartProxierHealthcheck", "StartKubeProxy", err.Error())
	case err = <-metricsErrCh:
		s.Recorder.Eventf(s.NodeRef, nil, api.EventTypeWarning, "FailedToStartMetricServer", "StartKubeProxy", err.Error())
	}
	return err
}

func (s *ProxyServer) birthCry() {
	s.Recorder.Eventf(s.NodeRef, nil, api.EventTypeNormal, "Starting", "StartKubeProxy", "")
}

// detectNodeIPs returns the proxier's "node IP" or IPs, and the IP family to use if the
// node turns out to be incapable of dual-stack. (Note that kube-proxy normally runs as
// dual-stack if the backend is capable of supporting both IP families, regardless of
// whether the node is *actually* configured as dual-stack or not.)

// (Note that on Linux, the node IPs are used only to determine whether a given
// LoadBalancerSourceRanges value matches the node or not. In particular, they are *not*
// used for NodePort handling.)
//
// The order of precedence is:
//  1. if bindAddress is not 0.0.0.0 or ::, then it is used as the primary IP.
//  2. if rawNodeIPs is not empty, then its address(es) is/are used
//  3. otherwise the node IPs are 127.0.0.1 and ::1
func detectNodeIPs(logger klog.Logger, rawNodeIPs []net.IP, nodeIPOverride []string) (v1.IPFamily, map[v1.IPFamily]net.IP) {
	primaryFamily := v1.IPv4Protocol
	nodeIPs := map[v1.IPFamily]net.IP{
		v1.IPv4Protocol: net.IPv4(127, 0, 0, 1),
		v1.IPv6Protocol: net.IPv6loopback,
	}

	if len(rawNodeIPs) > 0 {
		if !netutils.IsIPv4(rawNodeIPs[0]) {
			primaryFamily = v1.IPv6Protocol
		}
		nodeIPs[primaryFamily] = rawNodeIPs[0]
		if len(rawNodeIPs) > 1 {
			// If more than one address is returned, they are guaranteed to be of different families
			family := v1.IPv4Protocol
			if !netutils.IsIPv4(rawNodeIPs[1]) {
				family = v1.IPv6Protocol
			}
			nodeIPs[family] = rawNodeIPs[1]
		}
	}

	// If a bindAddress is passed, override the primary IP
	for _, addr := range nodeIPOverride {
		nodeIP := netutils.ParseIPSloppy(addr)
		if nodeIP != nil && !nodeIP.IsUnspecified() {
			if netutils.IsIPv4(nodeIP) {
				primaryFamily = v1.IPv4Protocol
			} else {
				primaryFamily = v1.IPv6Protocol
			}
			nodeIPs[primaryFamily] = nodeIP
		}
	}

	if nodeIPs[primaryFamily].IsLoopback() {
		logger.Info("Can't determine this node's IP, assuming loopback; if this is incorrect, please set the --bind-address flag")
	}
	return primaryFamily, nodeIPs
}

// getNodeIP returns IPs for the node with the provided name.  If
// required, it will wait for the node to be created.
func getNodeIPs(logger klog.Logger, client clientset.Interface, name string) []net.IP {
	var nodeIPs []net.IP
	backoff := wait.Backoff{
		Steps:    6,
		Duration: 1 * time.Second,
		Factor:   2.0,
		Jitter:   0.2,
	}

	err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		node, err := client.CoreV1().Nodes().Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			logger.Error(err, "Failed to retrieve node info")
			return false, nil
		}
		nodeIPs, err = utilnode.GetNodeHostIPs(node)
		if err != nil {
			logger.Error(err, "Failed to retrieve node IPs")
			return false, nil
		}
		return true, nil
	})
	if err == nil {
		logger.Info("Successfully retrieved node IP(s)", "IPs", nodeIPs)
	}
	return nodeIPs
}

/*
Copyright 2024 The Kubernetes Authors.
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

package v1alpha1

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/kube-proxy/config/v1alpha1"
	"k8s.io/kubernetes/pkg/proxy/apis/config"
	netutils "k8s.io/utils/net"
)

// Convert_v1alpha1_KubeProxyConfiguration_To_config_KubeProxyConfiguration is defined here, because public
// conversion is not auto-generated due to existing warnings.
func Convert_v1alpha1_KubeProxyConfiguration_To_config_KubeProxyConfiguration(in *v1alpha1.KubeProxyConfiguration, out *config.KubeProxyConfiguration, scope conversion.Scope) error {
	if err := autoConvert_v1alpha1_KubeProxyConfiguration_To_config_KubeProxyConfiguration(in, out, scope); err != nil {
		return err
	}

	out.Linux.OOMScoreAdj = in.OOMScoreAdj
	out.Linux.Conntrack = config.KubeProxyConntrackConfiguration(in.Conntrack)
	switch config.ProxyMode(in.Mode) {
	case config.ProxyModeNFTables:
		out.Linux.MasqueradeAll = in.NFTables.MasqueradeAll
	default:
		out.Linux.MasqueradeAll = in.IPTables.MasqueradeAll
	}

	if len(in.ClusterCIDR) != 0 {
		out.DetectLocal.ClusterCIDRs = strings.Split(in.ClusterCIDR, ",")
	} else {
		out.DetectLocal.ClusterCIDRs = []string{}
	}
	out.NodeIPOverride = strings.Split(in.BindAddress, ",")

	switch config.ProxyMode(in.Mode) {
	case config.ProxyModeIPVS:
		out.SyncPeriod = in.IPVS.SyncPeriod
		out.MinSyncPeriod = in.IPVS.MinSyncPeriod
	case config.ProxyModeNFTables:
		out.SyncPeriod = in.NFTables.SyncPeriod
		out.MinSyncPeriod = in.NFTables.MinSyncPeriod
	default:
		out.SyncPeriod = in.IPTables.SyncPeriod
		out.MinSyncPeriod = in.IPTables.MinSyncPeriod
	}

	out.IPVS.MasqueradeBit = in.IPTables.MasqueradeBit

	host, portStr, _ := net.SplitHostPort(in.HealthzBindAddress)
	port, _ := strconv.Atoi(portStr)
	if netutils.IsIPv4String(host) {
		out.HealthzBindAddresses = []string{fmt.Sprintf("%s/%d", host, 32)}
	} else {
		out.HealthzBindAddresses = []string{fmt.Sprintf("%s/%d", host, 128)}
	}
	out.HealthzBindPort = int32(port)

	host, portStr, _ = net.SplitHostPort(in.MetricsBindAddress)
	port, _ = strconv.Atoi(portStr)
	if netutils.IsIPv4String(host) {
		out.MetricsBindAddresses = []string{fmt.Sprintf("%s/%d", host, 32)}
	} else {
		out.MetricsBindAddresses = []string{fmt.Sprintf("%s/%d", host, 128)}
	}
	out.MetricsBindPort = int32(port)
	return nil
}

// Convert_config_KubeProxyConfiguration_To_v1alpha1_KubeProxyConfiguration is defined here, because public
// conversion is not auto-generated due to existing warnings.
func Convert_config_KubeProxyConfiguration_To_v1alpha1_KubeProxyConfiguration(in *config.KubeProxyConfiguration, out *v1alpha1.KubeProxyConfiguration, scope conversion.Scope) error {
	if err := autoConvert_config_KubeProxyConfiguration_To_v1alpha1_KubeProxyConfiguration(in, out, scope); err != nil {
		return err
	}

	out.ClusterCIDR = strings.Join(in.DetectLocal.ClusterCIDRs, ",")
	out.OOMScoreAdj = in.Linux.OOMScoreAdj
	out.Conntrack = v1alpha1.KubeProxyConntrackConfiguration(in.Linux.Conntrack)

	out.IPVS.SyncPeriod = in.SyncPeriod
	out.IPVS.MinSyncPeriod = in.MinSyncPeriod
	out.NFTables.SyncPeriod = in.SyncPeriod
	out.NFTables.MinSyncPeriod = in.MinSyncPeriod
	out.IPTables.SyncPeriod = in.SyncPeriod
	out.IPTables.MinSyncPeriod = in.MinSyncPeriod

	out.NFTables.MasqueradeAll = in.Linux.MasqueradeAll
	out.IPTables.MasqueradeAll = in.Linux.MasqueradeAll

	ip, _, _ := netutils.ParseCIDRSloppy(in.HealthzBindAddresses[0])
	out.HealthzBindAddress = net.JoinHostPort(ip.String(), strconv.Itoa(int(in.HealthzBindPort)))

	ip, _, _ = netutils.ParseCIDRSloppy(in.MetricsBindAddresses[0])
	out.MetricsBindAddress = net.JoinHostPort(ip.String(), strconv.Itoa(int(in.MetricsBindPort)))

	out.BindAddress = in.NodeIPOverride[0]
	return nil
}

// Convert_config_DetectLocalConfiguration_To_v1alpha1_DetectLocalConfiguration is defined here, because public
// conversion is not auto-generated due to existing warnings.
func Convert_config_DetectLocalConfiguration_To_v1alpha1_DetectLocalConfiguration(in *config.DetectLocalConfiguration, out *v1alpha1.DetectLocalConfiguration, scope conversion.Scope) error {
	if err := autoConvert_config_DetectLocalConfiguration_To_v1alpha1_DetectLocalConfiguration(in, out, scope); err != nil {
		return err
	}
	return nil
}

// Convert_v1alpha1_KubeProxyIPTablesConfiguration_To_config_KubeProxyIPTablesConfiguration is defined here, because public
// conversion is not auto-generated due to existing warnings.
func Convert_v1alpha1_KubeProxyIPTablesConfiguration_To_config_KubeProxyIPTablesConfiguration(in *v1alpha1.KubeProxyIPTablesConfiguration, out *config.KubeProxyIPTablesConfiguration, scope conversion.Scope) error {
	if err := autoConvert_v1alpha1_KubeProxyIPTablesConfiguration_To_config_KubeProxyIPTablesConfiguration(in, out, scope); err != nil {
		return err
	}
	return nil
}

// Convert_v1alpha1_KubeProxyIPVSConfiguration_To_config_KubeProxyIPVSConfiguration is defined here, because public
// conversion is not auto-generated due to existing warnings.
func Convert_v1alpha1_KubeProxyIPVSConfiguration_To_config_KubeProxyIPVSConfiguration(in *v1alpha1.KubeProxyIPVSConfiguration, out *config.KubeProxyIPVSConfiguration, scope conversion.Scope) error {
	if err := autoConvert_v1alpha1_KubeProxyIPVSConfiguration_To_config_KubeProxyIPVSConfiguration(in, out, scope); err != nil {
		return err
	}
	return nil
}

// Convert_config_KubeProxyIPVSConfiguration_To_v1alpha1_KubeProxyIPVSConfiguration is defined here, because public
// conversion is not auto-generated due to existing warnings.
func Convert_config_KubeProxyIPVSConfiguration_To_v1alpha1_KubeProxyIPVSConfiguration(in *config.KubeProxyIPVSConfiguration, out *v1alpha1.KubeProxyIPVSConfiguration, scope conversion.Scope) error {
	if err := autoConvert_config_KubeProxyIPVSConfiguration_To_v1alpha1_KubeProxyIPVSConfiguration(in, out, scope); err != nil {
		return err
	}
	return nil
}

// Convert_v1alpha1_KubeProxyNFTablesConfiguration_To_config_KubeProxyNFTablesConfiguration is defined here, because public
// conversion is not auto-generated due to existing warnings.
func Convert_v1alpha1_KubeProxyNFTablesConfiguration_To_config_KubeProxyNFTablesConfiguration(in *v1alpha1.KubeProxyNFTablesConfiguration, out *config.KubeProxyNFTablesConfiguration, scope conversion.Scope) error {
	if err := autoConvert_v1alpha1_KubeProxyNFTablesConfiguration_To_config_KubeProxyNFTablesConfiguration(in, out, scope); err != nil {
		return err
	}
	return nil
}

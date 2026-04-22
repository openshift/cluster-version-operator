package readiness

import (
	"context"
	"fmt"

	"k8s.io/client-go/dynamic"
)

// NetworkCheck verifies network plugin type, TLS profile, and proxy configuration.
type NetworkCheck struct{}

func (c *NetworkCheck) Name() string { return "network" }

func (c *NetworkCheck) Run(ctx context.Context, dc dynamic.Interface, current, target string) (map[string]any, error) {
	result := map[string]any{}
	var sectionErrors []map[string]any

	// Check Network configuration
	network, err := GetResource(ctx, dc, GVRNetwork, "cluster")
	if err != nil {
		return nil, fmt.Errorf("failed to get Network config: %w", err)
	}

	networkType := NestedString(network.Object, "status", "networkType")
	result["network_type"] = networkType

	// SDN deprecation warning
	if networkType == "OpenShiftSDN" {
		result["sdn_warning"] = "OpenShiftSDN is deprecated. Migration to OVN-Kubernetes is required before 4.17+."
	}

	// Check proxy
	proxy, err := GetResource(ctx, dc, GVRProxy, "cluster")
	if err != nil {
		SectionError(&sectionErrors, "proxy", err)
	} else {
		result["proxy"] = map[string]any{
			"http_proxy":  NestedString(proxy.Object, "spec", "httpProxy"),
			"https_proxy": NestedString(proxy.Object, "spec", "httpsProxy"),
			"no_proxy":    NestedString(proxy.Object, "spec", "noProxy"),
		}
	}

	// Check TLS profile from APIServer
	apiServer, err := GetResource(ctx, dc, GVRAPIServer, "cluster")
	if err != nil {
		SectionError(&sectionErrors, "apiserver_tls", err)
	} else {
		tlsProfile := NestedString(apiServer.Object, "spec", "tlsSecurityProfile", "type")
		if tlsProfile == "" {
			tlsProfile = "Intermediate"
		}
		result["tls_profile"] = tlsProfile
	}

	result["summary"] = map[string]any{
		"network_type": networkType,
		"is_sdn":       networkType == "OpenShiftSDN",
	}

	if len(sectionErrors) > 0 {
		result["errors"] = sectionErrors
	}

	return result, nil
}

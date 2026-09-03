package agenticrun

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "k8s.io/api/apps/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"

	"github.com/openshift/cluster-version-operator/pkg/agenticrun/bindata"
	i "github.com/openshift/cluster-version-operator/pkg/internal"
)

var tlsVersionToNginxProtocols = map[configv1.TLSProtocolVersion]string{
	configv1.VersionTLS10: "TLSv1 TLSv1.1 TLSv1.2 TLSv1.3",
	configv1.VersionTLS11: "TLSv1.1 TLSv1.2 TLSv1.3",
	configv1.VersionTLS12: "TLSv1.2 TLSv1.3",
	configv1.VersionTLS13: "TLSv1.3",
}

func resolveTLSProfileSpec(tlsSecurityProfile *configv1.TLSSecurityProfile) *configv1.TLSProfileSpec {
	if tlsSecurityProfile == nil {
		return configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	}
	if tlsSecurityProfile.Type == configv1.TLSProfileCustomType && tlsSecurityProfile.Custom != nil {
		return &tlsSecurityProfile.Custom.TLSProfileSpec
	}
	if spec, ok := configv1.TLSProfiles[tlsSecurityProfile.Type]; ok {
		return spec
	}
	klog.Warningf("Unknown TLS security profile type %q, falling back to Intermediate", tlsSecurityProfile.Type)
	return configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
}

func nginxTLSDirectives(profile *configv1.TLSProfileSpec) (sslProtocols, sslCiphers string) {
	sslProtocols, ok := tlsVersionToNginxProtocols[profile.MinTLSVersion]
	if !ok {
		klog.Warningf("No nginx protocol mapping for MinTLSVersion %q, falling back to TLS 1.2", profile.MinTLSVersion)
		sslProtocols = tlsVersionToNginxProtocols[configv1.VersionTLS12]
	}

	// TLS 1.3 ciphers (TLS_*) are not configurable via nginx ssl_ciphers —
	// they are always enabled when TLS 1.3 is negotiated.
	var ciphers []string
	skippedTLS13 := false
	for _, c := range profile.Ciphers {
		if strings.HasPrefix(c, "TLS_") {
			skippedTLS13 = true
		} else {
			ciphers = append(ciphers, c)
		}
	}
	if skippedTLS13 {
		klog.Warningf("Skipping TLS 1.3 ciphers from ssl_ciphers directive — nginx enables them automatically when TLS 1.3 is negotiated")
	}

	// Modern profile has only TLS 1.3 ciphers, which all get filtered above.
	// Nginx calls SSL_CTX_set_cipher_list (ngx_event_openssl.c ngx_ssl_ciphers)
	// at startup regardless of protocol version; OpenSSL rejects an empty string
	// (ssl_lib.c ssl_create_cipher_list). Use a single placeholder cipher — it is
	// never negotiated when only TLS 1.3 is active.
	if len(ciphers) == 0 {
		ciphers = []string{"ECDHE-ECDSA-AES128-GCM-SHA256"}
	}

	sslCiphers = strings.Join(ciphers, ":")
	return sslProtocols, sslCiphers
}

var consolePluginAssets = []string{
	"assets/namespace.yaml",
	"assets/serviceaccount.yaml",
	"assets/networkpolicy.yaml",
	"assets/networkpolicy-allow-console.yaml",
	"assets/configmap.yaml",
	"assets/deployment.yaml",
	"assets/service.yaml",
	"assets/consoleplugin.yaml",
}

func applyConsolePluginManifests(ctx context.Context, client ctrlruntimeclient.Client, image string, tlsProfile *configv1.TLSProfileSpec) error {
	sslProtocols, sslCiphers := nginxTLSDirectives(tlsProfile)

	configMapRaw := bindata.MustAsset("assets/configmap.yaml")
	rendered := strings.ReplaceAll(string(configMapRaw), "${SSL_PROTOCOLS}", sslProtocols)
	rendered = strings.ReplaceAll(rendered, "${SSL_CIPHERS}", sslCiphers)
	configHash := fmt.Sprintf("%x", sha256.Sum256([]byte(rendered)))

	for _, asset := range consolePluginAssets {
		var raw []byte
		if asset == "assets/configmap.yaml" {
			raw = []byte(rendered)
		} else {
			raw = bindata.MustAsset(asset)
		}

		if asset == "assets/deployment.yaml" {
			raw = []byte(strings.ReplaceAll(string(raw), "${IMAGE}", image))
			raw = []byte(strings.ReplaceAll(string(raw), "${CONFIG_HASH}", configHash))
		}

		obj := &unstructured.Unstructured{}
		if err := yaml.NewYAMLOrJSONDecoder(strings.NewReader(string(raw)), len(raw)).Decode(obj); err != nil {
			return fmt.Errorf("decoding %s: %w", asset, err)
		}

		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(obj.GroupVersionKind())
		err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(obj), existing)
		if err != nil {
			if ctrlruntimeclient.IgnoreNotFound(err) != nil {
				return fmt.Errorf("getting %s %s: %w", obj.GetKind(), obj.GetName(), err)
			}
			if err := client.Create(ctx, obj); err != nil {
				return fmt.Errorf("creating %s %s: %w", obj.GetKind(), obj.GetName(), err)
			}
			klog.V(i.Normal).Infof("Created console plugin %s %s", obj.GetKind(), obj.GetName())
			continue
		}

		if !needsUpdate(existing, obj) {
			klog.V(i.Debug).Infof("Console plugin %s %s is up to date", obj.GetKind(), obj.GetName())
			continue
		}
		obj.SetResourceVersion(existing.GetResourceVersion())
		if err := client.Update(ctx, obj); err != nil {
			return fmt.Errorf("updating %s %s: %w", obj.GetKind(), obj.GetName(), err)
		}
		if asset == "assets/configmap.yaml" {
			klog.Infof("Console plugin ConfigMap updated. Deployment rollout will follow")
		}
		klog.V(i.Normal).Infof("Updated console plugin %s %s", obj.GetKind(), obj.GetName())
	}
	return nil
}

func needsUpdate(existing, desired *unstructured.Unstructured) bool {
	if !reflect.DeepEqual(existing.Object["spec"], desired.Object["spec"]) {
		return true
	}
	return !reflect.DeepEqual(existing.Object["data"], desired.Object["data"])
}

func cleanupConsolePluginManifests(ctx context.Context, client ctrlruntimeclient.Client) error {
	for idx := len(consolePluginAssets) - 1; idx >= 0; idx-- {
		raw := bindata.MustAsset(consolePluginAssets[idx])

		obj := &unstructured.Unstructured{}
		if err := yaml.NewYAMLOrJSONDecoder(strings.NewReader(string(raw)), len(raw)).Decode(obj); err != nil {
			return fmt.Errorf("decoding %s: %w", consolePluginAssets[idx], err)
		}

		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(obj.GroupVersionKind())
		existing.SetName(obj.GetName())
		existing.SetNamespace(obj.GetNamespace())

		if err := client.Delete(ctx, existing); err != nil {
			if !kerrors.IsNotFound(err) {
				return fmt.Errorf("deleting %s %s: %w", obj.GetKind(), obj.GetName(), err)
			}
		} else {
			klog.V(i.Normal).Infof("Deleted console plugin %s %s", obj.GetKind(), obj.GetName())
		}
	}
	return nil
}

const (
	consolePluginName      = "cluster-update-console-plugin"
	consolePluginNamespace = "openshift-cluster-update-console-plugin"
)

func waitForPluginReady(ctx context.Context, client ctrlruntimeclient.Client) error {
	deployment := &appsv1.Deployment{}
	if err := client.Get(ctx, types.NamespacedName{Name: consolePluginName, Namespace: consolePluginNamespace}, deployment); err != nil {
		return fmt.Errorf("getting deployment: %w", err)
	}
	if deployment.Status.AvailableReplicas < 1 {
		return fmt.Errorf("deployment %s has no available replicas", consolePluginName)
	}
	return nil
}

func enableConsolePlugin(ctx context.Context, client ctrlruntimeclient.Client) error {
	console := &operatorv1.Console{}
	if err := client.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
		return fmt.Errorf("getting console operator config: %w", err)
	}
	for _, p := range console.Spec.Plugins {
		if p == consolePluginName {
			return nil
		}
	}
	plugins := append(console.Spec.Plugins, consolePluginName)
	patch, err := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"plugins": plugins,
		},
	})
	if err != nil {
		return fmt.Errorf("marshaling patch: %w", err)
	}
	if err := client.Patch(ctx, console, ctrlruntimeclient.RawPatch(types.MergePatchType, patch)); err != nil {
		return fmt.Errorf("enabling console plugin: %w", err)
	}
	klog.V(i.Normal).Infof("Enabled %s in console operator config", consolePluginName)
	return nil
}

func disableConsolePlugin(ctx context.Context, client ctrlruntimeclient.Client) error {
	console := &operatorv1.Console{}
	if err := client.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
		if kerrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("getting console operator config: %w", err)
	}
	filtered := make([]string, 0, len(console.Spec.Plugins))
	found := false
	for _, p := range console.Spec.Plugins {
		if p == consolePluginName {
			found = true
			continue
		}
		filtered = append(filtered, p)
	}
	if !found {
		return nil
	}
	patch, err := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"plugins": filtered,
		},
	})
	if err != nil {
		return fmt.Errorf("marshaling patch: %w", err)
	}
	if err := client.Patch(ctx, console, ctrlruntimeclient.RawPatch(types.MergePatchType, patch)); err != nil {
		return fmt.Errorf("disabling console plugin: %w", err)
	}
	klog.V(i.Normal).Infof("Disabled %s in console operator config", consolePluginName)
	return nil
}

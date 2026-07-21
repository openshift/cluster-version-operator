package agenticrun

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"

	operatorv1 "github.com/openshift/api/operator/v1"

	"github.com/openshift/cluster-version-operator/pkg/agenticrun/bindata"
	i "github.com/openshift/cluster-version-operator/pkg/internal"
)

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

func applyConsolePluginManifests(ctx context.Context, client ctrlruntimeclient.Client, image string) error {
	for _, asset := range consolePluginAssets {
		raw := bindata.MustAsset(asset)

		if asset == "assets/deployment.yaml" {
			raw = []byte(strings.ReplaceAll(string(raw), "${IMAGE}", image))
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

const consolePluginName = "cluster-update-console-plugin"

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

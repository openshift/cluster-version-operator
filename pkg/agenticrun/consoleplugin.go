package agenticrun

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"

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

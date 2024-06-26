package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/ghodss/yaml"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/clientcmd"
)

// main installs the CV CRD to a cluster for integration testing.
func main() {
	ctx := context.Background()
	log.SetFlags(0)
	kcfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{})
	cfg, err := kcfg.ClientConfig()
	if err != nil {
		log.Fatalf("cannot load config: %v", err)
	}

	client := apiext.NewForConfigOrDie(cfg)
	for _, path := range []string{
		"vendor/github.com/openshift/api/config/v1/zz_generated.crd-manifests/0000_00_cluster-version-operator_01_clusterversions-Default.crd.yaml",
		"vendor/github.com/openshift/api/config/v1/zz_generated.crd-manifests/0000_00_cluster-version-operator_01_clusteroperators.crd.yaml",
	} {
		var name string
		err := wait.PollUntilContextTimeout(ctx, time.Second, 30*time.Second, true, func(localCtx context.Context) (bool, error) {
			data, err := os.ReadFile(path)
			if err != nil {
				log.Fatalf("Unable to read %s: %v", path, err)
			}
			var crd v1.CustomResourceDefinition
			if err := yaml.Unmarshal(data, &crd); err != nil {
				log.Fatalf("Unable to parse CRD %s: %v", path, err)
			}
			name = crd.Name
			_, err = client.ApiextensionsV1().CustomResourceDefinitions().Create(localCtx, &crd, metav1.CreateOptions{})
			if errors.IsAlreadyExists(err) {
				return true, nil
			}
			if err != nil {
				log.Printf("error: failed creating CRD %s: %v", name, err)
				return false, nil
			}
			log.Printf("Installed %s CRD", crd.Name)
			return true, nil
		})
		if err != nil {
			log.Fatalf("Could not install %s CRD: %v", name, err)
		}
	}
}

package cvo

import (
	"context"
	"fmt"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	v1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/openshift/cluster-version-operator/test/utilities"
)

var _ = g.Describe(`[Jira:"Cluster Version Operator"] cluster-version-operator-tests`, func() {
	g.It("should support passing tests", func() {
		o.Expect(true).To(o.BeTrue())
	})
})

var _ = g.Describe("[Jira:Cluster Version Operator] The cluster version operator", g.Ordered, g.Label("cvo"), func() {
	defer g.GinkgoRecover()
	var client *v1.ConfigV1Client
	var kubeclient *kubernetes.Clientset

	g.BeforeAll(func() {
		client = utilities.MustGetV1Client()
		kubeclient = utilities.MustGetKubeClient()
	})

	g.It(`should not install resources annotated with release.openshift.io/delete=true`, g.Label("Conformance", "High", "42543"), func() {
		annotation := "release.openshift.io/delete"

		auths, err := client.Authentications().List(context.TODO(), metav1.ListOptions{})
		o.Expect(kerrors.IsNotFound(err)).To(o.BeFalse(), "The NotFound error should occur when listing authentications")

		g.By(fmt.Sprintf("checking if authentication with %s annotation exists", annotation))
		for _, auth := range auths.Items {
			if _, ok := auth.Annotations[annotation]; ok {
				o.Expect(ok).NotTo(o.BeTrue(), fmt.Sprintf("Unexpectedly installed authentication %s which has '%s' annotation", auth.Name, annotation))
			}
		}

		namespaces, err := kubeclient.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
		o.Expect(kerrors.IsNotFound(err)).To(o.BeFalse(), "The NotFound error should occur when listing namespaces")

		g.By(fmt.Sprintf("checking if special resources with %s annotation exist in all namespaces", annotation))
		for _, ns := range namespaces.Items {
			namespace := ns.Name
			fmt.Printf("namespace: %s\n", namespace)

			fmt.Println("	- Test services...")
			services, err := kubeclient.CoreV1().Services(namespace).List(context.TODO(), metav1.ListOptions{})
			o.Expect(kerrors.IsNotFound(err)).To(o.BeFalse(), "The NotFound error should occur when listing services")
			for _, service := range services.Items {
				if _, ok := service.Annotations[annotation]; ok {
					o.Expect(ok).NotTo(o.BeTrue(), fmt.Sprintf("Unexpectedly installed service %s which has '%s' annotation", service.Name, annotation))
				}
			}

			fmt.Println("	- Test RoleBinding...")
			rolebindings, err := kubeclient.RbacV1().RoleBindings(namespace).List(context.TODO(), metav1.ListOptions{})
			o.Expect(kerrors.IsNotFound(err)).To(o.BeFalse(), "The NotFound error should occur when listing rolebindings")
			for _, rb := range rolebindings.Items {
				if _, ok := rb.Annotations[annotation]; ok {
					o.Expect(ok).NotTo(o.BeTrue(), fmt.Sprintf("Unexpectedly installed RoleBinding %s which has '%s' annotation", rb.Name, annotation))
				}
			}

			fmt.Println("	- Test CronJob...")
			cronjobs, err := kubeclient.BatchV1().CronJobs(namespace).List(context.TODO(), metav1.ListOptions{})
			o.Expect(kerrors.IsNotFound(err)).To(o.BeFalse(), "The NotFound error should occur when listing cronjobs")
			for _, cj := range cronjobs.Items {
				if _, ok := cj.Annotations[annotation]; ok {
					o.Expect(ok).NotTo(o.BeTrue(), fmt.Sprintf("Unexpectedly installed CronJob %s which has %s annotation", cj.Name, annotation))
				}
			}

			fmt.Println("success")
		}
	})
})

package cvo

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	clientmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	applyconfigurationspolicyv1 "k8s.io/client-go/applyconfigurations/policy/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	v1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/openshift/cluster-version-operator/test/utilities"
)

func CreateServiceAccount(client *kubernetes.Clientset, accountName string, clusterRole string, namespace string) (token string, err error) {

	_, err = client.CoreV1().ServiceAccounts(namespace).Get(context.TODO(), accountName, metav1.GetOptions{})

	if err == nil {
		token, err := client.CoreV1().ServiceAccounts(namespace).CreateToken(context.TODO(), accountName, &authenticationv1.TokenRequest{}, metav1.CreateOptions{})
		return token.String(), err
	}

	account := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      accountName,
			Namespace: namespace,
		},
	}
	_, err = client.CoreV1().ServiceAccounts(namespace).Create(context.TODO(), account, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())

	rb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s:%s:%s", namespace, clusterRole, accountName),
			Namespace: namespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      accountName,
				Namespace: namespace,
			},
		},
	}
	_, err = client.RbacV1().ClusterRoleBindings().Create(context.TODO(), rb, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())

	newToken, err := client.CoreV1().ServiceAccounts(namespace).CreateToken(context.TODO(), accountName, &authenticationv1.TokenRequest{}, metav1.CreateOptions{})
	return newToken.String(), err
}

func DeleteServiceAccount(client *kubernetes.Clientset, accountName string, clusterRole string, namespace string) {
	name := fmt.Sprintf("%s:%s:%s", namespace, clusterRole, accountName)
	err := client.RbacV1().ClusterRoleBindings().Delete(context.TODO(), name, metav1.DeleteOptions{})
	if err != nil {
		panic("failed to delete ClusterRoleBindings")
	}

	err = client.CoreV1().ServiceAccounts(namespace).Delete(context.TODO(), accountName, metav1.DeleteOptions{})
	if err != nil {
		panic("failed to delete ServiceAccount")
	}
}

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

	g.It(`Precheck with oc adm upgrade recommend`, g.Label("Conformance", "Low", "70980"), func() {

		g.By("create a namespace")
		ns := "ns-70980"
		tmpNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
		kubeclient.CoreV1().Namespaces().Create(context.TODO(), tmpNs, metav1.CreateOptions{})

		defer func() {
			kubeclient.CoreV1().Namespaces().Delete(context.TODO(), ns, metav1.DeleteOptions{})
		}()

		g.By("create a deployment")
		deploymentName := "hello-openshift"
		containerName := "hello-openshift"
		containerImage := "openshift/hello-openshift:invaid"
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: deploymentName,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptr.To(int32(2)), // Number of desired replicas
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"app": containerName,
					},
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"app": containerName,
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  containerName,
								Image: containerImage,
								Ports: []corev1.ContainerPort{
									{
										ContainerPort: 80,
									},
								},
							},
						},
					},
				},
			},
		}
		kubeclient.AppsV1().Deployments(ns).Create(context.TODO(), deployment, metav1.CreateOptions{})

		defer func() {
			kubeclient.AppsV1().Deployments(ns).Delete(context.TODO(), deploymentName, metav1.DeleteOptions{})
		}()

		err := wait.Poll(1*time.Minute, 3*time.Minute, func() (bool, error) {
			allPods, err := kubeclient.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{})
			if err != nil {
				log.Fatalf("Error listing pods: %v", err)
			}
			for _, pod := range allPods.Items {
				if pod.Status.Phase == corev1.PodRunning {
					return true, errors.New("there are pods running: " + pod.Name)
				}
			}
			return true, nil
		})
		allPods, _ := kubeclient.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{})
		fmt.Printf("there are %v pods\n", len(allPods.Items))
		for _, pod := range allPods.Items {
			fmt.Printf("	- Pod: %s - %s\n", pod.Name, pod.Status.Phase)
		}
		o.Expect(kerrors.IsNotFound(err)).To(o.BeFalse(), "The NotFound error should not occur")

		g.By("create a PodDisruptionBudget")
		pdbName := "my-pdb"
		pdb := &applyconfigurationspolicyv1.PodDisruptionBudgetApplyConfiguration{
			ObjectMetaApplyConfiguration: &clientmetav1.ObjectMetaApplyConfiguration{
				Name:      &pdbName,
				Namespace: &ns,
			},
			Spec: &applyconfigurationspolicyv1.PodDisruptionBudgetSpecApplyConfiguration{
				MaxUnavailable: &intstr.IntOrString{
					Type:   intstr.Int,
					IntVal: 1,
				},
			},
		}
		kubeclient.PolicyV1().PodDisruptionBudgets(ns).Apply(context.TODO(), pdb, metav1.ApplyOptions{})

		defer func() {
			kubeclient.PolicyV1().PodDisruptionBudgets(ns).Delete(context.TODO(), pdbName, metav1.DeleteOptions{})
		}()

		g.By("wait some minutes, there is a critical issue for PDB")
		token, _ := CreateServiceAccount(kubeclient, "monitorer", "cluster-admin", ns)
		defer func() {
			DeleteServiceAccount(kubeclient, "monitorer", "cluster-admin", ns)
		}()
		// TODO: get alert
		fmt.Println(token)
	})
})

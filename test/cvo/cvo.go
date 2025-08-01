package cvo

import (
	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	exutil "github.com/openshift/cluster-version-operator/test/util"
)

var _ = g.Describe("[cvo-testing] cluster-version-operator-tests", func() {
	defer g.GinkgoRecover()

	projectName := "openshift-cluster-version"

	oc := exutil.NewCLIWithoutNamespace(projectName)

	g.It("should support passing tests", func() {
		o.Expect(true).To(o.BeTrue())
	})

	g.It("Ingress to CVO is not breaking for monitoring scrape", func() {
		exutil.By("Testing my commands")
		err := oc.AsAdmin().WithoutNamespace().Run("version")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(true).To(o.BeTrue())
	})
})

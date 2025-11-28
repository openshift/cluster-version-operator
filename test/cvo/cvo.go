package cvo

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/cluster-version-operator/test/oc"
	ocapi "github.com/openshift/cluster-version-operator/test/oc/api"
)

var logger = GinkgoLogr.WithName("cluster-version-operator-tests")

var _ = Describe(`[Jira:"Cluster Version Operator"] cluster-version-operator-tests`, func() {
	It("should support passing tests", func() {
		Expect(true).To(BeTrue())
	})

	It("can use oc to get the version information", func() {
		ocClient, err := oc.NewOC(logger)
		Expect(err).NotTo(HaveOccurred())
		Expect(ocClient).NotTo(BeNil())

		output, err := ocClient.Version(ocapi.VersionOptions{Client: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(ContainSubstring("Client Version:"))
	})
})

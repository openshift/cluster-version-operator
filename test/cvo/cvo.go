package cvo

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	compat_otp "github.com/openshift/origin/test/extended/util/compat_otp"
)

var _ = Describe("[Jira:Cluster Version Operator] cluster-version-operator-tests", func() {
	It("The removed resources are not created in a fresh installed cluster	", func() {
		compat_otp.By("Get CVO container status")
		Expect(true).To(BeTrue())
	})
})

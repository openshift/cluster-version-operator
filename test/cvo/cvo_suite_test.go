// This file is to support running tests with ginkgo-cli:
// ginkgo ./test/...
package cvo

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGinkgo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CVO Suite")
}

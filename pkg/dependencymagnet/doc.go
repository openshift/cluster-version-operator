// +build tools

// Package dependencymagnet is a dummy package so we can use Go's
// module framework to manage non-Go dependencies.
package dependencymagnet

import (
			 _ "github.com/openshift/build-machinery-go"
)

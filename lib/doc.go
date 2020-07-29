// Package lib defines a Manifest type.
//
// It also contains subpackages for reconciling in-cluster resources
// with local state.  The entrypoint is resourcebuilder, which consumes
// resourceread and resourceapply.  resourceapply in turn consumer
// resourcemerge.
package lib

package resourcebuilder

import (
	"context"
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/openshift/library-go/pkg/manifest"
)

var (
	// Mapper is default ResourceMapper.
	Mapper = NewResourceMapper()
)

// ResourceMapper maps {Group, Version} to a function that returns Interface and an error.
type ResourceMapper struct {
	l *sync.Mutex

	gvkToNew map[schema.GroupVersionKind]NewInterfaceFunc
}

// AddToMap adds all keys from caller to input.
// Locks the input ResourceMapper before adding the keys from caller.
func (rm *ResourceMapper) AddToMap(irm *ResourceMapper) {
	irm.l.Lock()
	defer irm.l.Unlock()
	for k, v := range rm.gvkToNew {
		irm.gvkToNew[k] = v
	}
}

// Exists returns true when gvk is known.
func (rm *ResourceMapper) Exists(gvk schema.GroupVersionKind) bool {
	_, ok := rm.gvkToNew[gvk]
	return ok
}

// RegisterGVK adds GVK to NewInterfaceFunc mapping.
// It does not lock before adding the mapping.
func (rm *ResourceMapper) RegisterGVK(gvk schema.GroupVersionKind, f NewInterfaceFunc) {
	rm.gvkToNew[gvk] = f
}

// NewResourceMapper returns a new map.
// This is required a we cannot push to uninitialized map.
func NewResourceMapper() *ResourceMapper {
	m := map[schema.GroupVersionKind]NewInterfaceFunc{}
	return &ResourceMapper{
		l:        &sync.Mutex{},
		gvkToNew: m,
	}
}

type MetaV1ObjectModifierFunc func(metav1.Object)

// NewInterfaceFunc returns an Interface.
// It requires rest Config that can be used to create a client
// and the Manifest.
type NewInterfaceFunc func(rest *rest.Config, m manifest.Manifest) Interface

// Mode is how this builder is being used.
type Mode int

const (
	UpdatingMode Mode = iota
	ReconcilingMode
	InitializingMode
	PrecreatingMode
)

type Interface interface {
	WithModifier(MetaV1ObjectModifierFunc) Interface
	WithMode(Mode) Interface
	Do(context.Context) error
}

// New returns Interface using the mapping stored in mapper for m Manifest.
func New(mapper *ResourceMapper, rest *rest.Config, m manifest.Manifest) (Interface, error) {
	f, ok := mapper.gvkToNew[m.GVK]
	if !ok {
		return nil, fmt.Errorf("No mapping found for gvk: %v", m.GVK)
	}
	return f(rest, m), nil
}

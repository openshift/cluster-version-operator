// Package customsignaturestore implements a signature store as configured by ClusterVersion.
package customsignaturestore

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/verify/store"
	"github.com/openshift/library-go/pkg/verify/store/parallel"
	"github.com/openshift/library-go/pkg/verify/store/sigstore"
)

type Store struct {
	// Name is the name of the ClusterVersion object that configures this store.
	Name string

	// Lister allows the store to fetch the current ClusterVersion configuration.
	Lister configv1listers.ClusterVersionLister

	// HTTPClient is called once for each Signatures call to ensure
	// requests are made with the currently-recommended parameters.
	HTTPClient sigstore.HTTPClient

	// lock allows the store to be locked while mutating or accessing internal state.
	lock sync.Mutex

	// customURIs tracks the most-recently retrieved ClusterVersion configuration.
	customURIs []*url.URL
}

// Signatures fetches signatures for the provided digest.
func (s *Store) Signatures(ctx context.Context, name string, digest string, fn store.Callback) error {
	uris, err := s.refreshConfiguration(ctx)
	if err != nil {
		return err
	}

	if uris == nil {
		return nil
	}

	if len(uris) == 0 {
		return errors.New("ClusterVersion spec.signatureStores is an empty array. Unset signatureStores entirely if you want to enable the default signature stores")
	}

	allDone := false

	wrapper := func(ctx context.Context, signature []byte, errIn error) (done bool, err error) {
		done, err = fn(ctx, signature, errIn)
		if done {
			allDone = true
		}
		return done, err
	}

	stores := make([]store.Store, 0, len(uris))
	for i := range uris {
		uri := *uris[i]
		stores = append(stores, &sigstore.Store{
			URI:        &uri,
			HTTPClient: s.HTTPClient,
		})
	}
	store := &parallel.Store{Stores: stores}
	if err := store.Signatures(ctx, name, digest, wrapper); err != nil || allDone {
		return err
	}
	return errors.New("ClusterVersion spec.signatureStores exhausted without finding a valid signature")
}

func (s *Store) refreshConfiguration(ctx context.Context) ([]*url.URL, error) {
	config, err := s.Lister.Get(s.Name)
	if err != nil {
		return nil, err
	}

	var uris []*url.URL
	if config.Spec.SignatureStores != nil {
		uris = make([]*url.URL, 0, len(config.Spec.SignatureStores))
		for _, store := range config.Spec.SignatureStores {
			uri, err := url.Parse(store.URL)
			if err != nil {
				return uris, err
			}

			uris = append(uris, uri)
		}
	}

	s.lock.Lock()
	defer s.lock.Unlock()
	s.customURIs = uris
	return uris, nil
}

// String returns a description of where this store finds
// signatures.
func (s *Store) String() string {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.customURIs == nil {
		return "ClusterVersion signatureStores unset, falling back to default stores"
	} else if len(s.customURIs) == 0 {
		return "0 ClusterVersion signatureStores"
	}
	uris := make([]string, 0, len(s.customURIs))
	for _, uri := range s.customURIs {
		uris = append(uris, uri.String())
	}
	return fmt.Sprintf("ClusterVersion signatureStores: %s", strings.Join(uris, ", "))
}

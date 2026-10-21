// Package customsignaturestore implements a signature store as configured by ClusterVersion.
package customsignaturestore

import (
	"context"
	"errors"
	"fmt"
	"net/url"

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
}

// Signatures fetches signatures for the provided digest.
func (s *Store) Signatures(ctx context.Context, name string, digest string, fn store.Callback) error {
	uris, err := s.refreshConfiguration()
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

func (s *Store) refreshConfiguration() ([]*url.URL, error) {
	config, err := s.Lister.Get(s.Name)
	if err != nil {
		return nil, err
	}

	var uris []*url.URL
	if config.Spec.SignatureStores != nil {
		uris = make([]*url.URL, 0, len(config.Spec.SignatureStores))
		for i, store := range config.Spec.SignatureStores {
			uri, err := url.Parse(store.URL)
			if err != nil {
				return uris, fmt.Errorf("invalid URL in ClusterVersion spec.signatureStores entry %d", i+1)
			}

			uris = append(uris, uri)
		}
	}

	return uris, nil
}

// String summarizes configuration without exposing store URLs or lookup details.
func (s *Store) String() string {
	uris, err := s.refreshConfiguration()
	if err != nil {
		return "unable to load ClusterVersion signatureStores"
	}

	if uris == nil {
		return "ClusterVersion signatureStores unset, falling back to default stores"
	} else if len(uris) == 0 {
		return "0 ClusterVersion signatureStores"
	}
	return fmt.Sprintf("ClusterVersion signatureStores configured: %d", len(uris))
}

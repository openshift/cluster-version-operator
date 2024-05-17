// Package customsignaturestore implements a signature store as configured by ClusterVersion.
package customsignaturestore

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	configv1 "github.com/openshift/api/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/verify/store"
	"github.com/openshift/library-go/pkg/verify/store/parallel"
	"github.com/openshift/library-go/pkg/verify/store/sigstore"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
)

type Store struct {
	// Name is the name of the ClusterVersion object that configures this store.
	Name string

	// ClusterVersionLister allows the store to fetch the current ClusterVersion configuration.
	ClusterVersionLister configv1listers.ClusterVersionLister

	// HTTPClient construct which respects the customstore CA certs and cluster proxy configuration
	HTTPClient func(string) (*http.Client, error)

	// lock allows the store to be locked while mutating or accessing internal state.
	lock sync.Mutex

	// customStores tracks the most-recently retrieved ClusterVersion configuration.
	customStores []configv1.SignatureStore
}

// Signatures fetches signatures for the provided digest.
func (s *Store) Signatures(ctx context.Context, name string, digest string, fn store.Callback) error {
	customStores, err := s.refreshConfiguration()
	if err != nil {
		return err
	} else if customStores == nil {
		return nil
	} else if len(customStores) == 0 {
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

	var errs []error
	stores := make([]store.Store, 0, len(customStores))
	for _, customStore := range customStores {
		uri, err := url.Parse(customStore.URL)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to parse the ClusterVersion spec.signatureStores %w", err))
			continue
		}
		newHttpClient, err := s.HTTPClient(customStore.CA.Name)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to process the ClusterVersion spec.signatureStores %w", err))
			continue
		}

		stores = append(stores, &sigstore.Store{
			URI:        uri,
			HTTPClient: func() (*http.Client, error) { return newHttpClient, nil }})
	}

	if len(stores) == 0 {
		return utilerrors.NewAggregate(errs)
	}
	store := &parallel.Store{Stores: stores}
	if err := store.Signatures(ctx, name, digest, wrapper); allDone {
		if len(errs) > 0 {
			klog.V(2).Infof("%s", utilerrors.NewAggregate(errs))
		}
		return nil
	} else if err != nil {
		errs = append(errs, err)
		return utilerrors.NewAggregate(errs)
	}

	errs = append(errs, errors.New("ClusterVersion spec.signatureStores exhausted without finding a valid signature"))
	return utilerrors.NewAggregate(errs)
}

// refreshConfiguration retrieves the latest configuration from the ClusterVersionLister
// and updates the customStores with the URL and CA information from the retrieved configuration.
// It returns the updated customStores slice and any error encountered during the retrieval process.
func (s *Store) refreshConfiguration() ([]configv1.SignatureStore, error) {

	config, err := s.ClusterVersionLister.Get(s.Name)
	if err != nil {
		return nil, err
	}

	var customStores = make([]configv1.SignatureStore, 0, len(config.Spec.SignatureStores))
	if config.Spec.SignatureStores != nil {
		for _, store := range config.Spec.SignatureStores {
			url := store.URL
			caCert := store.CA
			customStores = append(customStores, configv1.SignatureStore{URL: url, CA: caCert})
		}
	}

	s.lock.Lock()
	defer s.lock.Unlock()
	s.customStores = customStores
	return customStores, nil
}

// String returns a description of where this store finds
// signatures.
func (s *Store) String() string {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.customStores == nil {
		return "ClusterVersion signatureStores not set, falling back to default stores"
	} else if len(s.customStores) == 0 {
		return "0 ClusterVersion signatureStores"
	}
	customStores := make([]string, 0, len(s.customStores))
	for _, customStore := range s.customStores {
		customStores = append(customStores, customStore.URL)
	}
	return fmt.Sprintf("ClusterVersion signatureStores: %s", strings.Join(customStores, ", "))
}

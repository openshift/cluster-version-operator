// Package customsignaturestore implements a signature store as configured by ClusterVersion.
package customsignaturestore

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"golang.org/x/net/http/httpproxy"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/transport"
)

type Store struct {
	// Name is the name of the ClusterVersion object that configures this store.
	Name string

	// ClusterVersionLister allows the store to fetch the current ClusterVersion configuration.
	ClusterVersionLister configv1listers.ClusterVersionLister

	// ConfigMapLister allows the store to fetch certificate
	// authority ConfigMaps for per-store trust.
	ConfigMapLister corev1listers.ConfigMapNamespaceLister

	// ProxyLister allows the store to fetch the current ClusterVersion proxy configuration.
	proxyLister configv1listers.ProxyLister

	// lock allows the store to be locked while mutating or accessing internal state.
	lock sync.Mutex

	// customStores tracks the most-recently retrieved ClusterVersion configuration.
	customStores []configv1.SignatureStore
}

// Signatures fetches signatures for the provided digest.
func (s *Store) Signatures(ctx context.Context, name string, digest string, fn store.Callback) error {
	customStores, err := s.refreshConfiguration(ctx)
	if err != nil {
		return err
	} else if customStores == nil {
		return nil
	} else if len(customStores) == 0 {
		return errors.New("ClusterVersion spec.signatureStores is an empty array.  Unset signatureStores entirely if you want to to enable the default signature stores.")
	}

	allDone := false

	wrapper := func(ctx context.Context, signature []byte, errIn error) (done bool, err error) {
		done, err = fn(ctx, signature, errIn)
		if done {
			allDone = true
		}
		return done, err
	}

	stores := make([]store.Store, 0, len(customStores))
	for _, customStore := range customStores {
		uri, err := url.Parse(customStore.URL)
		if err != nil {
			return err
		}
		newHttpClient, err := s.HTTPClient(customStore.CA.Name)
		if err != nil {
			return err
		}
		if customStore.CA.Name != "" {
			stores = append(stores, &sigstore.Store{
				URI:        uri,
				HTTPClient: func() (*http.Client, error) { return newHttpClient, nil }})
		}

	}
	if len(stores) == 0 {
		return errors.New("ClusterVersion spec.signatureStores exhausted without finding a valid signature")
	}
	store := &parallel.Store{Stores: stores}
	if err := store.Signatures(ctx, name, digest, wrapper); err != nil || allDone {
		return err
	}
	return nil
}

// refreshConfiguration fetches the current ClusterVersion configuration
// and saves the information to a local copy
func (s *Store) refreshConfiguration(ctx context.Context) ([]configv1.SignatureStore, error) {
	config, err := s.ClusterVersionLister.Get(s.Name)
	if err != nil {
		return nil, err
	}

	//Save configv1.SignatureStore.URL and configv1.SignatureStore.CA information
	// from configv1.SignatureStore to customStores variable
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
		caName := customStore.CA.Name
		if caName != "" {
			customStores = append(customStores, caName)
		}
	}
	return fmt.Sprintf("ClusterVersion signatureStores: %s", strings.Join(customStores, ", "))
}

// HTTPClient provides a method for generating an HTTP client
// with the proxy and trust settings, if set in the cluster.
func (s *Store) HTTPClient(certAuthName string) (*http.Client, error) {
	transportOption, err := s.getTransport(certAuthName)
	if err != nil {
		return nil, err
	}
	transportConfig := &transport.Config{Transport: transportOption}
	transport, err := transport.New(transportConfig)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Transport: transport,
	}, nil
}

// getTransport constructs an HTTP transport configuration, including
// any custom proxy configuration.
func (s *Store) getTransport(certAuthName string) (*http.Transport, error) {
	transport := &http.Transport{}

	proxyConfig, err := s.getProxyConfig()
	if err != nil {
		return transport, err
	} else if proxyConfig != nil {
		proxyFunc := proxyConfig.ProxyFunc()
		transport.Proxy = func(req *http.Request) (*url.URL, error) {
			if req == nil {
				return nil, errors.New("cannot calculate proxy URI for nil request")
			}
			return proxyFunc(req.URL)
		}
	}

	tlsConfig, err := s.getTLSConfig(certAuthName)
	if err != nil {
		return transport, err
	} else if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig
	}
	return transport, err
}

func (s *Store) getTLSConfig(certAuthName string) (*tls.Config, error) {
	cm, err := s.ConfigMapLister.Get(certAuthName)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	certPool := x509.NewCertPool()

	if cm.Data["ca.crt"] != "" {
		if ok := certPool.AppendCertsFromPEM([]byte(cm.Data["ca.crt"])); !ok {
			return nil, fmt.Errorf("unable to add ca.crt certificates from %s/%s", cm.Namespace, cm.Name)
		}
	} else {
		return nil, nil
	}

	tlsConfig := &tls.Config{
		RootCAs: certPool,
	}

	return tlsConfig, nil
}

// getProxyConfig returns a proxy configuration.  It can be nil if
// does not exist or there is an error.
func (s *Store) getProxyConfig() (*httpproxy.Config, error) {
	proxy, err := s.proxyLister.Get("cluster")

	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &httpproxy.Config{
		HTTPProxy:  proxy.Status.HTTPProxy,
		HTTPSProxy: proxy.Status.HTTPSProxy,
		NoProxy:    proxy.Status.NoProxy,
	}, nil
}

package cvo

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"golang.org/x/net/http/httpproxy"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/openshift/cluster-version-operator/pkg/version"
)

// Returns a User-Agent to be used for outgoing HTTP requests.
//
// https://www.rfc-editor.org/rfc/rfc7231#section-5.5.3
func (optr *Operator) getUserAgent() string {
	token := "ClusterVersionOperator"
	productVersion := version.Version
	return fmt.Sprintf("%s/%s", token, productVersion)
}

// getTransport constructs an HTTP transport configuration, including
// any custom proxy configuration.
func (optr *Operator) getTransport() (*http.Transport, error) {
	transport := &http.Transport{}

	proxyConfig, err := optr.getProxyConfig()
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

	tlsConfig, err := optr.getTLSConfig()
	if err != nil {
		return transport, err
	} else if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig
	}

	return transport, err
}

// getProxyConfig returns a proxy configuration.  It can be nil if
// does not exist or there is an error.
func (optr *Operator) getProxyConfig() (*httpproxy.Config, error) {
	proxy, err := optr.proxyLister.Get("cluster")

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

func (optr *Operator) getTLSConfig() (*tls.Config, error) {
	cm, err := optr.cmConfigManagedLister.Get("trusted-ca-bundle")
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	certPool := x509.NewCertPool()

	if cm.Data["ca-bundle.crt"] != "" {
		if ok := certPool.AppendCertsFromPEM([]byte(cm.Data["ca-bundle.crt"])); !ok {
			return nil, fmt.Errorf("unable to add ca-bundle.crt certificates")
		}
	} else {
		return nil, nil
	}

	config := &tls.Config{
		RootCAs: certPool,
	}

	return config, nil
}

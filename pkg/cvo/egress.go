package cvo

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"

	"k8s.io/apimachinery/pkg/api/errors"
)

// getHTTPSProxyURL returns a url.URL object for the configured
// https proxy only. It can be nil if does not exist or there is an error.
func (optr *Operator) getHTTPSProxyURL() (*url.URL, error) {
	proxy, err := optr.proxyLister.Get("cluster")

	if errors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if proxy.Status.HTTPSProxy != "" {
		proxyURL, err := url.Parse(proxy.Status.HTTPSProxy)
		if err != nil {
			return nil, err
		}
		return proxyURL, nil
	}
	return nil, nil
}

func (optr *Operator) getTLSConfig() (*tls.Config, error) {
	cm, err := optr.cmConfigManagedLister.Get("trusted-ca-bundle")
	if errors.IsNotFound(err) {
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

package resourceapply

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	securityv1 "github.com/openshift/api/security/v1"
	securityclientv1 "github.com/openshift/client-go/security/clientset/versioned/typed/security/v1"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
)

type modifiedSccTracker struct {
	sync.Mutex
	lastUpdated time.Time
	names       map[string]time.Time
}

var modifiedSccs modifiedSccTracker

func init() {
	modifiedSccs = modifiedSccTracker{
		lastUpdated: time.Now().UTC(),
		names:       make(map[string]time.Time),
	}
}

// ModifiedSCCs returns the list of SCCs that have been modified in the cluster
func ModifiedSCCs() []string {
	modifiedSccs.Lock()
	defer modifiedSccs.Unlock()
	names := make([]string, 0, len(modifiedSccs.names))

	// discard any records older than 10 minutes before last cache write, they are
	// likely stale (this should not happen, but we are being defensive)
	staleThreshold := modifiedSccs.lastUpdated.Add(-10 * time.Minute)
	for name, saw := range modifiedSccs.names {
		if saw.Before(staleThreshold) {
			delete(modifiedSccs.names, name)
		} else {
			names = append(names, name)
		}
	}

	sort.Strings(names)
	return names
}

// markSccModifiedInCluster marks the SCC as modified in the cluster
func trackModifiedScc(name string, modified bool) {
	modifiedSccs.Lock()
	defer modifiedSccs.Unlock()
	modifiedSccs.lastUpdated = time.Now().UTC()
	if modified {
		modifiedSccs.names[name] = modifiedSccs.lastUpdated
	} else {
		delete(modifiedSccs.names, name)
	}
}

// ApplySecurityContextConstraintsv1 applies the required SecurityContextConstraints to the cluster.
func ApplySecurityContextConstraintsv1(ctx context.Context, client securityclientv1.SecurityContextConstraintsGetter, required *securityv1.SecurityContextConstraints, reconciling bool) (*securityv1.SecurityContextConstraints, bool, error) {
	existing, err := client.SecurityContextConstraints().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("SCC %s not found, creating", required.Name)
		actual, err := client.SecurityContextConstraints().Create(ctx, required, metav1.CreateOptions{})
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	reconcile, requiredMerges := resourcemerge.EnsureSecurityContextConstraints(*existing, *required)

	if reconciling {
		trackModifiedScc(required.Name, requiredMerges)
	}

	if reconcile == nil {
		return existing, false, nil
	}

	if reconciling {
		if diff := cmp.Diff(existing, reconcile); diff != "" {
			klog.V(2).Infof("Updating SCC %s/%s due to diff: %v", required.Namespace, required.Name, diff)
		} else {
			klog.V(2).Infof("Updating SCC %s/%s with empty diff: possible hotloop after wrong comparison", required.Namespace, required.Name)
		}
	}

	actual, err := client.SecurityContextConstraints().Update(ctx, reconcile, metav1.UpdateOptions{})
	return actual, true, err
}

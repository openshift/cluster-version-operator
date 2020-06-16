package verifyconfigmap

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"
)

// ReleaseLabelConfigMap is a label applied to a configmap inside the
// openshift-config-managed namespace that indicates it contains signatures
// for release image digests. Any binaryData key that starts with the digest
// is added to the list of signatures checked.
const ReleaseLabelConfigMap = "release.openshift.io/verification-signatures"

// Store abstracts retrieving signatures from config maps on a cluster.
type Store struct {
	client corev1client.ConfigMapsGetter
	ns     string

	limiter *rate.Limiter
	lock    sync.Mutex
	last    []corev1.ConfigMap
}

// NewStore returns a store that can retrieve or persist signatures on a
// cluster. If limiter is not specified it defaults to one call every 30 seconds.
func NewStore(client corev1client.ConfigMapsGetter, limiter *rate.Limiter) *Store {
	if limiter == nil {
		limiter = rate.NewLimiter(rate.Every(30*time.Second), 1)
	}
	return &Store{
		client:  client,
		ns:      "openshift-config-managed",
		limiter: limiter,
	}
}

// String displays information about this source for human review.
func (s *Store) String() string {
	return fmt.Sprintf("config maps in %s with label %q", s.ns, ReleaseLabelConfigMap)
}

// rememberMostRecentConfigMaps stores a set of config maps containing
// signatures.
func (s *Store) rememberMostRecentConfigMaps(last []corev1.ConfigMap) {
	names := make([]string, 0, len(last))
	for _, cm := range last {
		names = append(names, cm.ObjectMeta.Name)
	}
	sort.Strings(names)
	s.lock.Lock()
	defer s.lock.Unlock()
	klog.V(4).Infof("remember most recent signature config maps: %s", strings.Join(names, " "))
	s.last = last
}

// mostRecentConfigMaps returns the last cached version of config maps
// containing signatures.
func (s *Store) mostRecentConfigMaps() []corev1.ConfigMap {
	s.lock.Lock()
	defer s.lock.Unlock()
	klog.V(4).Info("use cached most recent signature config maps")
	return s.last
}

// digestToKeyPrefix changes digest to use '-' in place of ':',
// {algo}-{hash} instead of {algo}:{hash}, because colons are not
// allowed in ConfigMap keys.
func digestToKeyPrefix(digest string) (string, error) {
	parts := strings.SplitN(digest, ":", 3)
	if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) == 0 {
		return "", fmt.Errorf("the provided digest must be of the form ALGO:HASH")
	}
	algo, hash := parts[0], parts[1]
	return fmt.Sprintf("%s-%s", algo, hash), nil
}

// DigestSignatures returns a list of signatures that match the request
// digest out of config maps labelled with ReleaseLabelConfigMap in the
// openshift-config-managed namespace.
func (s *Store) DigestSignatures(ctx context.Context, digest string) ([][]byte, error) {
	// avoid repeatedly reloading config maps
	items := s.mostRecentConfigMaps()
	r := s.limiter.Reserve()
	if items == nil || r.OK() {
		configMaps, err := s.client.ConfigMaps(s.ns).List(metav1.ListOptions{
			LabelSelector: ReleaseLabelConfigMap,
		})
		if err != nil {
			s.rememberMostRecentConfigMaps([]corev1.ConfigMap{})
			return nil, err
		}
		items = configMaps.Items
		s.rememberMostRecentConfigMaps(configMaps.Items)
	}

	prefix, err := digestToKeyPrefix(digest)
	if err != nil {
		return nil, err
	}

	var signatures [][]byte
	for _, cm := range items {
		klog.V(4).Infof("searching for %s in signature config map %s", prefix, cm.ObjectMeta.Name)
		for k, v := range cm.BinaryData {
			if strings.HasPrefix(k, prefix) {
				klog.V(4).Infof("key %s from signature config map %s matches %s", k, cm.ObjectMeta.Name, digest)
				signatures = append(signatures, v)
			}
		}
	}
	return signatures, nil
}

// Store attempts to persist the provided signatures into a form DigestSignatures will
// retrieve.
func (s *Store) Store(ctx context.Context, signaturesByDigest map[string][][]byte) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: s.ns,
			Name:      "signatures-managed",
			Labels: map[string]string{
				ReleaseLabelConfigMap: "",
			},
		},
		BinaryData: make(map[string][]byte),
	}
	count := 0
	for digest, signatures := range signaturesByDigest {
		prefix, err := digestToKeyPrefix(digest)
		if err != nil {
			return err
		}
		for i := 0; i < len(signatures); i++ {
			cm.BinaryData[fmt.Sprintf("%s-%d", prefix, i)] = signatures[i]
			count += 1
		}
	}
	return retry.OnError(
		retry.DefaultRetry,
		func(err error) bool { return errors.IsConflict(err) || errors.IsAlreadyExists(err) },
		func() error {
			existing, err := s.client.ConfigMaps(s.ns).Get(cm.Name, metav1.GetOptions{})
			if errors.IsNotFound(err) {
				_, err := s.client.ConfigMaps(s.ns).Create(cm)
				if err != nil {
					klog.V(4).Infof("create signature cache config map %s in namespace %s with %d signatures", cm.ObjectMeta.Name, s.ns, count)
				}
				return err
			}
			if err != nil {
				return err
			}
			existing.Labels = cm.Labels
			existing.BinaryData = cm.BinaryData
			existing.Data = cm.Data
			_, err = s.client.ConfigMaps(s.ns).Upgrade(existing)
			if err != nil {
				klog.V(4).Infof("upgrade signature cache config map %s in namespace %s with %d signatures", cm.ObjectMeta.Name, s.ns, count)
			}
			return err
		},
	)
}

package cincinnati

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
	"k8s.io/klog/v2"
)

const (
	// GraphMediaType is the media-type specified in the HTTP Accept header
	// of requests sent to the Cincinnati-v1 Graph API.
	GraphMediaType = "application/json"

	// Timeout when calling upstream Cincinnati stack.
	getUpdatesTimeout = time.Minute * 60
)

// Client is a Cincinnati client which can be used to fetch update graphs from
// an upstream Cincinnati stack.
type Client struct {
	id        uuid.UUID
	transport *http.Transport

	// userAgent configures the User-Agent header for upstream
	// requests.  If empty, the User-Agent header will not be
	// populated.
	userAgent string
}

// NewClient creates a new Cincinnati client with the given client identifier.
func NewClient(id uuid.UUID, transport *http.Transport, userAgent string) Client {
	return Client{
		id:        id,
		transport: transport,
		userAgent: userAgent,
	}
}

// Error is returned when are unable to get updates.
type Error struct {
	// Reason is the reason suggested for the ClusterOperator status condition.
	Reason string

	// Message is the message suggested for the ClusterOperator status condition.
	Message string

	// cause is the upstream error, if any, being wrapped by this error.
	cause error
}

// Error serializes the error as a string, to satisfy the error interface.
func (err *Error) Error() string {
	return fmt.Sprintf("%s: %s", err.Reason, err.Message)
}

// GetUpdates fetches the current and next-applicable update payloads from the specified
// upstream Cincinnati stack given the current version, desired architecture, and channel.
// The command:
//
//  1. Downloads the update graph from the requested URI for the requested desired arch and channel.
//  2. Finds the current version entry under .nodes.
//  3. If a transition from single to multi architecture has been requested, the only valid
//     version is the current version so it's returned.
//  4. Finds recommended next-hop updates by searching .edges for updates from the current
//     version. Returns a slice of target Releases with these unconditional recommendations.
//  5. Finds conditionally recommended next-hop updates by searching .conditionalEdges for
//     updates from the current version.  Returns a slice of ConditionalUpdates with these
//     conditional recommendations.
func (c Client) GetUpdates(ctx context.Context, uri *url.URL, desiredArch, currentArch, channel string,
	version semver.Version) (configv1.Release, []configv1.Release, []configv1.ConditionalUpdate, error) {

	var current configv1.Release

	releaseArch := desiredArch
	if desiredArch == string(configv1.ClusterVersionArchitectureMulti) {
		releaseArch = "multi"
	}

	// Prepare parametrized cincinnati query.
	queryParams := uri.Query()
	queryParams.Add("arch", releaseArch)
	queryParams.Add("channel", channel)
	queryParams.Add("id", c.id.String())
	queryParams.Add("version", version.String())
	uri.RawQuery = queryParams.Encode()

	// Download the update graph.
	req, err := http.NewRequest("GET", uri.String(), nil)
	if err != nil {
		return current, nil, nil, &Error{Reason: "InvalidRequest", Message: err.Error(), cause: err}
	}

	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	req.Header.Set("Accept", GraphMediaType)
	if c.transport != nil && c.transport.TLSClientConfig != nil {
		if c.transport.TLSClientConfig.ClientCAs == nil {
			klog.V(2).Infof("Using a root CA pool with 0 root CA subjects to request updates from %s", uri)
		} else {
			klog.V(2).Infof("Using a root CA pool with %n root CA subjects to request updates from %s", len(c.transport.TLSClientConfig.RootCAs.Subjects()), uri)
		}
	}

	if c.transport != nil && c.transport.Proxy != nil {
		proxy, err := c.transport.Proxy(req)
		if err == nil && proxy != nil {
			klog.V(2).Infof("Using proxy %s to request updates from %s", proxy.Host, uri)
		}
	}

	client := http.Client{}
	if c.transport != nil {
		client.Transport = c.transport
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, getUpdatesTimeout)
	defer cancel()
	resp, err := client.Do(req.WithContext(timeoutCtx))
	if err != nil {
		return current, nil, nil, &Error{Reason: "RemoteFailed", Message: err.Error(), cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return current, nil, nil, &Error{Reason: "ResponseFailed", Message: fmt.Sprintf("unexpected HTTP status: %s", resp.Status)}
	}

	// Parse the graph.
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return current, nil, nil, &Error{Reason: "ResponseFailed", Message: err.Error(), cause: err}
	}

	var graph graph
	if err = json.Unmarshal(body, &graph); err != nil {
		return current, nil, nil, &Error{Reason: "ResponseInvalid", Message: err.Error(), cause: err}
	}

	// Find the current version within the graph.
	var currentIdx int
	found := false
	for i, node := range graph.Nodes {
		if version.EQ(node.Version) {
			currentIdx = i
			found = true
			current, err = convertRetrievedUpdateToRelease(graph.Nodes[i])
			if err != nil {
				return current, nil, nil, &Error{
					Reason:  "ResponseInvalid",
					Message: fmt.Sprintf("invalid current node: %s", err),
				}
			}

			// Migrating from single to multi architecture. Only valid update for required heterogeneous graph
			// is heterogeneous version of current version.
			if desiredArch == string(configv1.ClusterVersionArchitectureMulti) && currentArch != desiredArch {
				return current, []configv1.Release{current}, nil, nil
			}
			break
		}
	}
	if !found {
		return current, nil, nil, &Error{
			Reason:  "VersionNotFound",
			Message: fmt.Sprintf("currently reconciling cluster version %s not found in the %q channel", version, channel),
		}
	}

	// Find the children of the current version.
	var nextIdxs []int
	for _, edge := range graph.Edges {
		if edge.Origin == currentIdx {
			nextIdxs = append(nextIdxs, edge.Destination)
		}
	}

	var updates []configv1.Release
	for _, i := range nextIdxs {
		update, err := convertRetrievedUpdateToRelease(graph.Nodes[i])
		if err != nil {
			return current, nil, nil, &Error{
				Reason:  "ResponseInvalid",
				Message: fmt.Sprintf("invalid recommended update node: %s", err),
			}
		}
		updates = append(updates, update)
	}

	var conditionalUpdates []configv1.ConditionalUpdate
	for _, conditionalEdges := range graph.ConditionalEdges {
		for _, edge := range conditionalEdges.Edges {
			if version.String() == edge.From {
				var target *node
				for i, node := range graph.Nodes {
					if node.Version.String() == edge.To {
						target = &graph.Nodes[i]
						break
					}
				}
				if target == nil {
					return current, updates, nil, &Error{
						Reason:  "ResponseInvalid",
						Message: fmt.Sprintf("no node for conditional update %s", edge.To),
					}
				}
				update, err := convertRetrievedUpdateToRelease(*target)
				if err != nil {
					return current, updates, nil, &Error{
						Reason:  "ResponseInvalid",
						Message: fmt.Sprintf("invalid conditional update node: %s", err),
					}
				}
				conditionalUpdates = append(conditionalUpdates, configv1.ConditionalUpdate{
					Release: update,
					Risks:   conditionalEdges.Risks,
				})
			}
		}
	}

	for i := len(updates) - 1; i >= 0; i-- {
		for _, conditionalUpdate := range conditionalUpdates {
			if conditionalUpdate.Release.Image == updates[i].Image {
				klog.Warningf("Update to %s listed as both a conditional and unconditional update; preferring the conditional update.", conditionalUpdate.Release.Version)
				updates = append(updates[:i], updates[i+1:]...)
				break
			}
		}
	}

	if len(updates) == 0 {
		updates = nil
	}

	for i := len(conditionalUpdates) - 1; i >= 0; i-- {
		for j, risk := range conditionalUpdates[i].Risks {
			conditionalUpdates[i].Risks[j].MatchingRules, err = clusterconditions.PruneInvalid(ctx, risk.MatchingRules)
			if len(conditionalUpdates[i].Risks[j].MatchingRules) == 0 {
				klog.Warningf("Conditional update to %s, risk %q, has empty pruned matchingRules; dropping this target to avoid rejections when pushing to the Kubernetes API server. Pruning results: %s", conditionalUpdates[i].Release.Version, risk.Name, err)
				conditionalUpdates = append(conditionalUpdates[:i], conditionalUpdates[i+1:]...)
				break
			} else if err != nil {
				klog.Warningf("Conditional update to %s, risk %q, has pruned matchingRules (although other valid, recognized matchingRules were given, and are sufficient to keep the conditional update): %s", conditionalUpdates[i].Release.Version, risk.Name, err)
			}
		}
	}

	targets := make(map[string]int, len(conditionalUpdates))
	for _, conditionalUpdate := range conditionalUpdates {
		targets[conditionalUpdate.Release.Image]++
	}

	for i := len(conditionalUpdates) - 1; i >= 0; i-- {
		if targets[conditionalUpdates[i].Release.Image] > 1 {
			klog.Warningf("Upstream declares %d conditional updates to %s; dropping them all.", targets[conditionalUpdates[i].Release.Image], conditionalUpdates[i].Release.Version)
			conditionalUpdates = append(conditionalUpdates[:i], conditionalUpdates[i+1:]...)
		}
	}

	if len(conditionalUpdates) == 0 {
		conditionalUpdates = nil
	}

	return current, updates, conditionalUpdates, nil
}

type graph struct {
	Nodes            []node
	Edges            []edge
	ConditionalEdges []conditionalEdges `json:"conditionalEdges"`
}

type node struct {
	Version  semver.Version    `json:"version"`
	Image    string            `json:"payload"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type edge struct {
	Origin      int
	Destination int
}

type conditionalEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type conditionalEdges struct {
	Edges []conditionalEdge                `json:"edges"`
	Risks []configv1.ConditionalUpdateRisk `json:"risks"`
}

// UnmarshalJSON unmarshals an edge in the update graph. The edge's JSON
// representation is a two-element array of indices, but Go's representation is
// a struct with two elements so this custom unmarshal method is required.
func (e *edge) UnmarshalJSON(data []byte) error {
	var fields []int
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	if len(fields) != 2 {
		return fmt.Errorf("expected 2 fields, found %d", len(fields))
	}

	e.Origin = fields[0]
	e.Destination = fields[1]

	return nil
}

func convertRetrievedUpdateToRelease(update node) (configv1.Release, error) {
	cvoUpdate := configv1.Release{
		Version: update.Version.String(),
		Image:   update.Image,
	}
	if urlString, ok := update.Metadata["url"]; ok {
		_, err := url.Parse(urlString)
		if err != nil {
			return cvoUpdate, fmt.Errorf("invalid URL for %s: %s", cvoUpdate.Version, err)
		}
		cvoUpdate.URL = configv1.URL(urlString)
	}
	if channels, ok := update.Metadata["io.openshift.upgrades.graph.release.channels"]; ok {
		cvoUpdate.Channels = strings.Split(channels, ",")
		sort.Strings(cvoUpdate.Channels)
	}
	return cvoUpdate, nil
}

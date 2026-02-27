// Package cincinnati provides a client for interacting with the Cincinnati update service.
//
// Cincinnati is the OpenShift update recommendation service that provides update graphs
// indicating which cluster versions can safely upgrade to which target versions. The service
// returns both unconditional update recommendations (via edges in the update graph) and
// conditional update recommendations (via conditionalEdges with associated risks).
//
// The Client fetches update graphs from an upstream Cincinnati server using the Cincinnati v1
// Graph API. It handles:
//   - Querying for available updates based on the current cluster version, architecture, and channel
//   - Parsing the update graph response to identify recommended next-hop updates
//   - Processing conditional updates that may have associated risks requiring evaluation
//   - Handling transitions between single and multi-architecture deployments
//
// Update graphs consist of nodes (representing cluster versions) and edges (representing
// valid upgrade paths). Conditional edges represent upgrades that are only recommended
// when certain risk conditions are evaluated and deemed acceptable.
package cincinnati

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
)

const (
	// GraphMediaType is the media type specified in the HTTP Accept header
	// for requests sent to the Cincinnati v1 Graph API.
	GraphMediaType = "application/json"

	// getUpdatesTimeout is the maximum duration allowed for a single request
	// to the upstream Cincinnati service.
	getUpdatesTimeout = time.Minute * 60
)

// Client is a Cincinnati client for fetching update graphs from an upstream
// Cincinnati service. It maintains connection settings and cluster identification
// for making requests to the update service.
type Client struct {
	// id is the unique cluster identifier sent with each update request.
	id uuid.UUID

	// transport configures HTTP transport settings including TLS configuration,
	// proxy settings, and connection pooling.
	transport *http.Transport

	// userAgent is the User-Agent header value for upstream requests.
	// If empty, the User-Agent header will not be set.
	userAgent string

	// conditionRegistry evaluates conditional update risks and prunes invalid
	// matching rules before storing conditional updates.
	conditionRegistry clusterconditions.ConditionRegistry
}

// NewClient creates a new Cincinnati client with the specified configuration.
// The id parameter uniquely identifies the cluster making update requests.
// The transport parameter configures HTTP settings including TLS and proxy configuration.
// The userAgent parameter sets the User-Agent header for requests (optional).
// The conditionRegistry parameter is used to evaluate and prune conditional update risks.
func NewClient(id uuid.UUID, transport *http.Transport, userAgent string, conditionRegistry clusterconditions.ConditionRegistry) Client {
	return Client{
		id:                id,
		transport:         transport,
		userAgent:         userAgent,
		conditionRegistry: conditionRegistry,
	}
}

// Error represents a failure when fetching updates from the Cincinnati service.
type Error struct {
	// Reason is the machine-readable reason code for the ClusterVersion
	// RetrievedUpdates condition (e.g., "RemoteFailed", "ResponseInvalid").
	Reason string

	// Message is the human-readable error message for the ClusterVersion
	// RetrievedUpdates condition.
	Message string

	// cause is the underlying error that triggered this failure, if any.
	cause error
}

// Error serializes the error as a string, to satisfy the error interface.
func (err *Error) Error() string {
	return fmt.Sprintf("%s: %s", err.Reason, err.Message)
}

// GetUpdates retrieves available cluster updates from the Cincinnati service.
// It returns the current release information, a list of unconditionally recommended
// updates, a list of conditionally recommended updates, and any error encountered.
//
// The method performs the following steps:
//  1. Constructs a query to the Cincinnati service including the cluster architecture,
//     update channel, cluster ID, and current version.
//  2. Downloads and parses the update graph from the service.
//  3. Locates the current version within the graph nodes.
//  4. For single-to-multi architecture transitions, returns only the current version
//     in multi-architecture form as the sole valid update.
//  5. Identifies unconditional update recommendations by following edges from the
//     current version to destination nodes.
//  6. Identifies conditional update recommendations from conditionalEdges, filtering
//     out duplicates and pruning invalid risk matching rules.
//
// Parameters:
//   - ctx: Context for the HTTP request, allowing cancellation
//   - uri: Base URI of the Cincinnati service
//   - desiredArch: Target architecture for updates (e.g., "amd64", "Multi")
//   - currentArch: Current cluster architecture
//   - channel: Update channel name (e.g., "stable-4.14", "fast-4.15")
//   - version: Current semantic version of the cluster
//
// Returns:
//   - Current release metadata from the update graph
//   - Slice of unconditionally recommended update releases (nil if none)
//   - Slice of conditionally recommended updates with associated risks (nil if none)
//   - Error if the request fails or the response is invalid
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
			//nolint:staticcheck // SA1019: TLSClientConfig.RootCAs.Subjects() is deprecated because
			// "if s was returned by SystemCertPool, Subjects will not include the system roots"
			// but that should not apply for us, we construct it ourselves in Operator.getTLSConfig()
			klog.V(2).Infof("Using a root CA pool with %d root CA subjects to request updates from %s", len(c.transport.TLSClientConfig.RootCAs.Subjects()), uri)
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
	defer func() {
		if err := resp.Body.Close(); err != nil {
			klog.Errorf("Failed to close response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return current, nil, nil, &Error{Reason: "ResponseFailed", Message: fmt.Sprintf("unexpected HTTP status: %s", resp.Status)}
	}

	// Parse the graph.
	body, err := io.ReadAll(resp.Body)
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
			conditionalUpdates[i].Risks[j].MatchingRules, err = c.conditionRegistry.PruneInvalid(ctx, risk.MatchingRules)
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

// graph represents the update graph structure returned by the Cincinnati service.
// It defines all available cluster versions and the valid upgrade paths between them.
type graph struct {
	// Nodes contains all cluster version releases available in this channel.
	Nodes []node

	// Edges defines unconditional upgrade paths as index pairs referencing Nodes.
	// Each edge indicates a recommended upgrade from one version to another.
	Edges []edge

	// ConditionalEdges defines upgrade paths that require risk evaluation.
	// These upgrades are only recommended if their associated risks are acceptable.
	ConditionalEdges []conditionalEdges `json:"conditionalEdges"`
}

// node represents a single cluster version in the update graph.
type node struct {
	// Version is the semantic version of this release.
	Version semver.Version `json:"version"`

	// Image is the release image pullspec (payload) for this version.
	Image string `json:"payload"`

	// Metadata contains additional release information such as URL, architecture,
	// and supported update channels.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// edge represents an unconditional upgrade path between two versions in the graph.
// It is serialized as a two-element array [origin, destination] in JSON.
type edge struct {
	// Origin is the index of the source version node.
	Origin int

	// Destination is the index of the target version node.
	Destination int
}

// conditionalEdge represents a single conditional upgrade path between two versions
// identified by their version strings rather than node indices.
type conditionalEdge struct {
	// From is the semantic version string of the source release.
	From string `json:"from"`

	// To is the semantic version string of the target release.
	To string `json:"to"`
}

// conditionalEdges groups a set of conditional upgrade edges with their shared risks.
// All edges in this group are subject to the same risk conditions.
type conditionalEdges struct {
	// Edges contains the conditional upgrade paths sharing these risks.
	Edges []conditionalEdge `json:"edges"`

	// Risks defines the conditions that must be evaluated to determine if
	// these conditional updates are recommended for a particular cluster.
	Risks []configv1.ConditionalUpdateRisk `json:"risks"`
}

// UnmarshalJSON deserializes an edge from its JSON representation.
// Edges are represented in JSON as two-element arrays [origin, destination],
// but are stored in Go as a struct with named fields, requiring this custom
// unmarshaling logic.
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

// convertRetrievedUpdateToRelease converts a Cincinnati graph node to a ClusterVersion Release.
// It combines the node's version and image with metadata parsed from the node's metadata map.
func convertRetrievedUpdateToRelease(update node) (configv1.Release, error) {
	release, err := ParseMetadata(update.Metadata)
	release.Version = update.Version.String()
	release.Image = update.Image
	return release, err
}

// ParseMetadata extracts release metadata from a node's metadata map.
// It parses the release URL, architecture, and supported update channels.
// The version and image fields are not populated by this function and must
// be set separately by the caller.
//
// Returns a partially populated Release struct and an aggregated error containing
// all parsing errors encountered. Parsing continues even after errors to extract
// as much valid metadata as possible.
func ParseMetadata(metadata map[string]interface{}) (configv1.Release, error) {
	release := configv1.Release{}
	errs := []error{}
	if urlInterface, hasURL := metadata["url"]; hasURL {
		if urlString, isString := urlInterface.(string); isString {
			if _, err := url.Parse(urlString); err == nil {
				release.URL = configv1.URL(urlString)
			} else {
				errs = append(errs, fmt.Errorf("invalid release URL: %s", err))
			}
		} else {
			errs = append(errs, fmt.Errorf("URL is not a string: %v", urlInterface))
		}
	}
	if archInterface, hasArch := metadata["release.openshift.io/architecture"]; hasArch {
		if arch, isString := archInterface.(string); isString {
			if arch == "multi" {
				release.Architecture = configv1.ClusterVersionArchitectureMulti
			} else {
				errs = append(errs, fmt.Errorf("unrecognized release.openshift.io/architecture value %q", arch))
			}
		} else {
			errs = append(errs, fmt.Errorf("release.openshift.io/architecture is not a string: %v", archInterface))
		}
	}
	if channelsInterface, hasChannels := metadata["io.openshift.upgrades.graph.release.channels"]; hasChannels {
		if channelsString, isString := channelsInterface.(string); isString {
			if len(channelsString) == 0 {
				errs = append(errs, fmt.Errorf("io.openshift.upgrades.graph.release.channels is an empty string"))
			} else {
				channels := strings.Split(channelsString, ",")
				if len(channels) == 0 {
					errs = append(errs, fmt.Errorf("no comma-delimited channels in io.openshift.upgrades.graph.release.channels %q", channelsString))
				} else {
					for i := len(channels) - 1; i >= 0; i-- {
						if len(channels[i]) == 0 {
							errs = append(errs, fmt.Errorf("io.openshift.upgrades.graph.release.channels entry %d is an empty string: %q", i, channelsString))
							channels = append(channels[:i], channels[i+1:]...)
						}
					}
					if len(channels) == 0 {
						errs = append(errs, fmt.Errorf("no non-empty channels in io.openshift.upgrades.graph.release.channels %q", channels))
					} else {
						sort.Strings(channels)
						release.Channels = channels
					}
				}
			}
		} else {
			errs = append(errs, fmt.Errorf("io.openshift.upgrades.graph.release.channels is not a string: %v", channelsInterface))
		}
	}
	klog.V(2).Infof("parsed metadata: URL %q, architecture %q, channels %v, errors %v", release.URL, release.Architecture, release.Channels, errs)
	return release, errors.Join(errs...)
}

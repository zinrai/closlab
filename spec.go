package main

import "fmt"

const (
	// ContainerImage is the Docker image used for all BIRD nodes.
	ContainerImage = "ghcr.io/zinrai/docker-ubuntu-bird3:ubuntu-resolute"

	// TopologyName is the containerlab topology name (used for prefixing container names).
	TopologyName = "clos"

	// BridgeName is the host Linux bridge used for external connectivity.
	// It must be created on the host before `containerlab deploy`.
	BridgeName = "ext"

	// ExternalNetworkGateway is the gateway IP on the host side of the bridge.
	ExternalNetworkGateway = "172.31.255.1"

	// ExternalNetworkPrefix is the /24 prefix used for the external network.
	ExternalNetworkPrefix = "172.31.255"
)

// ClabTopo is the top-level containerlab topology document.
type ClabTopo struct {
	Name     string       `yaml:"name"`
	Topology ClabTopology `yaml:"topology"`
}

// ClabTopology holds nodes and links.
type ClabTopology struct {
	Nodes map[string]ClabNode `yaml:"nodes"`
	Links []ClabLink          `yaml:"links,omitempty"`
}

// ClabNode is one entry under topology.nodes.
type ClabNode struct {
	Kind  string   `yaml:"kind"`
	Image string   `yaml:"image,omitempty"`
	Binds []string `yaml:"binds,omitempty"`
	Exec  []string `yaml:"exec,omitempty"`
}

// ClabLink is one entry under topology.links (brief form).
type ClabLink struct {
	Endpoints []string `yaml:"endpoints"`
}

// Neighbor represents a BGP neighbor.
type Neighbor struct {
	Name         string
	Interface    string
	PeerASN      int    // Peer's AS number
	PeerLLA      string // Peer's link-local address with interface scope (e.g., fe80::1%eth0)
	LocalLLA     string // Local link-local address (without interface scope)
	ImportFilter string
	ExportFilter string
	MaxPrefix    int
}

// ExternalRouterIP returns the external network IP for a router by index.
// router0 -> 172.31.255.2, router1 -> 172.31.255.3, etc.
func ExternalRouterIP(index int) string {
	return fmt.Sprintf("%s.%d", ExternalNetworkPrefix, index+2)
}

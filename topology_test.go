package main

import (
	"strings"
	"testing"
)

// testTemplates returns minimal templates for testing.
func testTemplates() *Templates {
	minimalTemplate := `router id {{ .RouterID }};
define LOCAL_AS = {{ .ASN }};
{{ range .Neighbors }}
protocol bgp {{ .Name }} {
        neighbor {{ .PeerLLA }} as {{ .PeerASN }};
        local {{ .LocalLLA }} as LOCAL_AS;
}
{{ end }}
`
	return &Templates{
		Spine:  minimalTemplate,
		Leaf:   minimalTemplate,
		BL:     minimalTemplate,
		ToR:    minimalTemplate,
		Server: minimalTemplate,
		Router: minimalTemplate,
	}
}

func TestRouterIDUniqueness(t *testing.T) {
	cfg := DefaultConfig()
	ids := make(map[string]string)

	// Spines
	for i := 0; i < cfg.NumSpines; i++ {
		id := SpineRouterID(i)
		name := "spine" + string(rune('0'+i))
		if existing, ok := ids[id]; ok {
			t.Errorf("Duplicate router ID %s: %s and %s", id, existing, name)
		}
		ids[id] = name
	}

	// Leafs
	for p := 0; p < cfg.NumLeafPairs; p++ {
		for l := 1; l <= 2; l++ {
			id := LeafRouterID(p, l)
			name := "leaf"
			if existing, ok := ids[id]; ok {
				t.Errorf("Duplicate router ID %s: %s and %s", id, existing, name)
			}
			ids[id] = name
		}
	}

	// ToRs
	for i := 0; i < cfg.TotalToRs(); i++ {
		id := ToRRouterID(i)
		name := "tor"
		if existing, ok := ids[id]; ok {
			t.Errorf("Duplicate router ID %s: %s and %s", id, existing, name)
		}
		ids[id] = name
	}

	// Border Leafs
	for i := 0; i < cfg.NumBorderLeafs; i++ {
		id := BorderLeafRouterID(i)
		name := "bl"
		if existing, ok := ids[id]; ok {
			t.Errorf("Duplicate router ID %s: %s and %s", id, existing, name)
		}
		ids[id] = name
	}

	// Routers
	for i := 0; i < cfg.NumRouters; i++ {
		id := RouterRouterID(i)
		name := "router"
		if existing, ok := ids[id]; ok {
			t.Errorf("Duplicate router ID %s: %s and %s", id, existing, name)
		}
		ids[id] = name
	}

	// Servers
	for i := 0; i < cfg.TotalServers(); i++ {
		id := ServerRouterID(i)
		name := "server"
		if existing, ok := ids[id]; ok {
			t.Errorf("Duplicate router ID %s: %s and %s", id, existing, name)
		}
		ids[id] = name
	}
}

// TestLinkSymmetry walks every clab link and verifies both endpoints appear as
// nodes in the topology (catches typos in interface naming and missing reverse links).
func TestLinkSymmetry(t *testing.T) {
	cfg := DefaultConfig()
	topo := NewTopology(cfg, testTemplates())
	clab, err := topo.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Track (node, interface) seen across all links — each endpoint should be unique
	// (a p2p link can't share an interface with another link).
	seen := make(map[string]string)
	for _, link := range clab.Topology.Links {
		if len(link.Endpoints) != 2 {
			t.Errorf("Link does not have exactly 2 endpoints: %v", link.Endpoints)
			continue
		}
		for _, ep := range link.Endpoints {
			parts := strings.SplitN(ep, ":", 2)
			if len(parts) != 2 {
				t.Errorf("Invalid endpoint format: %s", ep)
				continue
			}
			node := parts[0]
			if _, ok := clab.Topology.Nodes[node]; !ok {
				t.Errorf("Link endpoint %s references undefined node %s", ep, node)
			}
			if other, dup := seen[ep]; dup {
				t.Errorf("Endpoint %s appears in multiple links (also in link with %s)", ep, other)
			}
			seen[ep] = ep
		}
	}
}

// TestNodeHasBirdConfigBind ensures each kind:linux node bind-mounts its BIRD config
// to /etc/bird/bird.conf.
func TestNodeHasBirdConfigBind(t *testing.T) {
	cfg := DefaultConfig()
	topo := NewTopology(cfg, testTemplates())
	clab, err := topo.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	for name, node := range clab.Topology.Nodes {
		if node.Kind != "linux" {
			continue
		}
		want := name + ".conf:/etc/bird/bird.conf"
		found := false
		for _, b := range node.Binds {
			if strings.HasSuffix(b, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Node %s missing bind for BIRD config (looking for *%s)", name, want)
		}

		hasMkdir := false
		hasBird := false
		for _, cmd := range node.Exec {
			if cmd == "mkdir -p /run/bird" {
				hasMkdir = true
			}
			if cmd == "bird -c /etc/bird/bird.conf" {
				hasBird = true
			}
		}
		if !hasMkdir {
			t.Errorf("Node %s missing mkdir for /run/bird", name)
		}
		if !hasBird {
			t.Errorf("Node %s missing bird start command", name)
		}
	}
}

// TestMACCommandsGenerated checks that every kind:linux node received at least one
// `ip link set dev ... address 02:...` exec (i.e. MAC override was scheduled).
func TestMACCommandsGenerated(t *testing.T) {
	cfg := DefaultConfig()
	topo := NewTopology(cfg, testTemplates())
	clab, err := topo.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	for name, node := range clab.Topology.Nodes {
		if node.Kind != "linux" {
			continue
		}

		found := false
		for _, cmd := range node.Exec {
			if strings.Contains(cmd, "ip link set dev") && strings.Contains(cmd, "address 02:") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Node %s missing MAC setting command", name)
		}
	}
}

// TestPeerLLAInBirdConfig sanity-checks that rendered BIRD configs reference an
// LLA neighbor with interface scope (the whole point of the MAC/LL pairing).
func TestPeerLLAInBirdConfig(t *testing.T) {
	cfg := DefaultConfig()
	topo := NewTopology(cfg, testTemplates())
	if _, err := topo.Build(); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	configs := topo.GetBirdConfigs()
	spine0Config, ok := configs["spine0"]
	if !ok {
		t.Fatal("Missing spine0 config")
	}

	if !strings.Contains(spine0Config, "neighbor fe80::") {
		t.Errorf("spine0 config missing LLA neighbor")
	}
	if !strings.Contains(spine0Config, "%") {
		t.Errorf("spine0 config missing interface scope")
	}
	if !strings.Contains(spine0Config, " as ") {
		t.Errorf("spine0 config missing 'as' clause")
	}
}

// TestMACUniqueness verifies that no two interfaces in the topology end up with the
// same MAC address (which would cause LL collisions and broken BGP).
func TestMACUniqueness(t *testing.T) {
	cfg := DefaultConfig()
	topo := NewTopology(cfg, testTemplates())
	clab, err := topo.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	macs := make(map[string]string) // MAC -> "node:interface"

	for nodeName, node := range clab.Topology.Nodes {
		for _, cmd := range node.Exec {
			if !strings.Contains(cmd, "ip link set dev") || !strings.Contains(cmd, "address") {
				continue
			}
			parts := strings.Fields(cmd)
			if len(parts) < 7 {
				continue
			}
			iface := parts[4]
			mac := parts[6]
			key := nodeName + ":" + iface

			if existing, ok := macs[mac]; ok {
				t.Errorf("Duplicate MAC %s: %s and %s", mac, existing, key)
			}
			macs[mac] = key
		}
	}
}

// TestExternalNetworkBridge verifies that enabling external network adds a kind:bridge
// node and a link from each router's ext0 to it.
func TestExternalNetworkBridge(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExternalNetwork = true
	cfg.ExternalInterface = "ens3"
	topo := NewTopology(cfg, testTemplates())
	clab, err := topo.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	bridge, ok := clab.Topology.Nodes[BridgeName]
	if !ok {
		t.Fatalf("Bridge node %q not present when external network enabled", BridgeName)
	}
	if bridge.Kind != "bridge" {
		t.Errorf("Bridge node kind = %q, want %q", bridge.Kind, "bridge")
	}

	for rt := 0; rt < cfg.NumRouters; rt++ {
		want := "router" + string(rune('0'+rt)) + ":ext0"
		found := false
		for _, link := range clab.Topology.Links {
			for _, ep := range link.Endpoints {
				if ep == want {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("No link found containing endpoint %q", want)
		}
	}
}

package main

import (
	"fmt"
)

// Topology builds a Clos network topology for containerlab.
type Topology struct {
	config      Config
	templates   *Templates
	nodeOrder   []string                       // deterministic node insertion order
	clabNodes   map[string]ClabNode            // node name -> clab node definition
	links       []ClabLink                     // containerlab links (one per p2p)
	birdConfigs map[string]string              // node name -> rendered bird.conf
	linkID      uint32                         // counter for deterministic MAC generation
	macCmds     map[string][]string            // node name -> MAC/LL setup commands
	peerLLAs    map[string]map[string]peerInfo // node -> interface -> peer info
}

// peerInfo holds peer information for a link.
type peerInfo struct {
	PeerLLA  string
	PeerASN  int
	LocalLLA string
}

// NewTopology creates a new topology builder.
func NewTopology(cfg Config, templates *Templates) *Topology {
	return &Topology{
		config:      cfg,
		templates:   templates,
		clabNodes:   make(map[string]ClabNode),
		birdConfigs: make(map[string]string),
		macCmds:     make(map[string][]string),
		peerLLAs:    make(map[string]map[string]peerInfo),
	}
}

// Build generates the complete containerlab topology document.
func (t *Topology) Build() (ClabTopo, error) {
	if err := t.buildSpines(); err != nil {
		return ClabTopo{}, err
	}
	if err := t.buildLeafs(); err != nil {
		return ClabTopo{}, err
	}
	if err := t.buildBorderLeafs(); err != nil {
		return ClabTopo{}, err
	}
	if err := t.buildToRs(); err != nil {
		return ClabTopo{}, err
	}
	if err := t.buildServers(); err != nil {
		return ClabTopo{}, err
	}
	if err := t.buildRouters(); err != nil {
		return ClabTopo{}, err
	}

	// If external network is enabled, add a kind:bridge node representing the host bridge
	// and link each router's ext0 to it. The bridge itself must be pre-created on the host.
	if t.config.ExternalNetwork {
		t.clabNodes[BridgeName] = ClabNode{Kind: "bridge"}
		t.nodeOrder = append(t.nodeOrder, BridgeName)

		for rtIdx := 0; rtIdx < t.config.NumRouters; rtIdx++ {
			rtName := fmt.Sprintf("router%d", rtIdx)
			t.links = append(t.links, ClabLink{
				Endpoints: []string{
					fmt.Sprintf("%s:ext0", rtName),
					fmt.Sprintf("%s:%s_ext", BridgeName, rtName),
				},
			})
		}
	}

	return ClabTopo{
		Name: TopologyName,
		Topology: ClabTopology{
			Nodes: t.clabNodes,
			Links: t.links,
		},
	}, nil
}

// GetBirdConfigs returns the generated BIRD configuration files.
func (t *Topology) GetBirdConfigs() map[string]string {
	return t.birdConfigs
}

// addLink creates a p2p link between two nodes, generates deterministic MACs and EUI-64
// link-local addresses for each side, and stores the per-side peer info for later BGP
// neighbor rendering.
func (t *Topology) addLink(
	node1, if1 string, asn1 int,
	node2, if2 string, asn2 int,
) {
	mac1 := GenerateMAC(t.linkID)
	mac2 := GenerateMAC(t.linkID + 1)
	t.linkID += 2

	lla1 := MACToLLA(mac1)
	lla2 := MACToLLA(mac2)

	// `ip -6 addr replace` is idempotent: clab brings interfaces up before exec runs,
	// so the kernel may have already auto-generated an LL from the MAC we then change.
	t.macCmds[node1] = append(t.macCmds[node1],
		fmt.Sprintf("ip link set dev %s address %s", if1, mac1),
		fmt.Sprintf("ip -6 addr replace %s/64 dev %s", lla1, if1),
	)
	t.macCmds[node2] = append(t.macCmds[node2],
		fmt.Sprintf("ip link set dev %s address %s", if2, mac2),
		fmt.Sprintf("ip -6 addr replace %s/64 dev %s", lla2, if2),
	)

	t.links = append(t.links, ClabLink{
		Endpoints: []string{
			fmt.Sprintf("%s:%s", node1, if1),
			fmt.Sprintf("%s:%s", node2, if2),
		},
	})

	if t.peerLLAs[node1] == nil {
		t.peerLLAs[node1] = make(map[string]peerInfo)
	}
	if t.peerLLAs[node2] == nil {
		t.peerLLAs[node2] = make(map[string]peerInfo)
	}

	t.peerLLAs[node1][if1] = peerInfo{
		PeerLLA:  FormatLLAWithInterface(lla2, if1),
		PeerASN:  asn2,
		LocalLLA: lla1.String(),
	}
	t.peerLLAs[node2][if2] = peerInfo{
		PeerLLA:  FormatLLAWithInterface(lla1, if2),
		PeerASN:  asn1,
		LocalLLA: lla2.String(),
	}
}

// getPeerInfo returns the peer LLA, peer ASN, and local LLA for a given node and interface.
func (t *Topology) getPeerInfo(nodeName, ifName string) (peerLLA string, peerASN int, localLLA string) {
	if nodeInfo, ok := t.peerLLAs[nodeName]; ok {
		if info, ok := nodeInfo[ifName]; ok {
			return info.PeerLLA, info.PeerASN, info.LocalLLA
		}
	}
	return "", 0, ""
}

func (t *Topology) buildSpines() error {
	for i := 0; i < t.config.NumSpines; i++ {
		name := fmt.Sprintf("spine%d", i)
		routerID := SpineRouterID(i)

		// Connect to Leafs
		for pairIdx := 0; pairIdx < t.config.NumLeafPairs; pairIdx++ {
			leafASN := LeafASN(pairIdx)
			for leafNum := 1; leafNum <= 2; leafNum++ {
				leafName := fmt.Sprintf("leaf%d-as%d", leafNum, leafASN)
				myIf := fmt.Sprintf("lf%d", pairIdx*2+(leafNum-1))
				peerIf := fmt.Sprintf("sp%d", i)

				t.addLink(name, myIf, ASNSpine, leafName, peerIf, leafASN)
			}
		}

		// Connect to Border Leafs
		for blIdx := 0; blIdx < t.config.NumBorderLeafs; blIdx++ {
			myIf := fmt.Sprintf("bl%d", blIdx)
			peerIf := fmt.Sprintf("sp%d", i)

			t.addLink(name, myIf, ASNSpine, fmt.Sprintf("bl%d", blIdx), peerIf, ASNBorderLeaf)
		}

		var neighbors []Neighbor

		// Leaf neighbors
		for pairIdx := 0; pairIdx < t.config.NumLeafPairs; pairIdx++ {
			leafASN := LeafASN(pairIdx)
			for leafNum := 1; leafNum <= 2; leafNum++ {
				myIf := fmt.Sprintf("lf%d", pairIdx*2+(leafNum-1))
				peerLLA, peerASN, localLLA := t.getPeerInfo(name, myIf)

				neighbors = append(neighbors, Neighbor{
					Name:         fmt.Sprintf("leaf%d_as%d", leafNum, leafASN),
					Interface:    myIf,
					PeerASN:      peerASN,
					PeerLLA:      peerLLA,
					LocalLLA:     localLLA,
					ImportFilter: "spine_import",
					ExportFilter: "spine_export",
					MaxPrefix:    500,
				})
			}
		}

		// Border Leaf neighbors
		for blIdx := 0; blIdx < t.config.NumBorderLeafs; blIdx++ {
			myIf := fmt.Sprintf("bl%d", blIdx)
			peerLLA, peerASN, localLLA := t.getPeerInfo(name, myIf)

			neighbors = append(neighbors, Neighbor{
				Name:         fmt.Sprintf("bl%d", blIdx),
				Interface:    myIf,
				PeerASN:      peerASN,
				PeerLLA:      peerLLA,
				LocalLLA:     localLLA,
				ImportFilter: "spine_import",
				ExportFilter: "spine_export",
				MaxPrefix:    100,
			})
		}

		if err := t.addNodeConfig(name, routerID, "spine", ASNSpine, neighbors, false); err != nil {
			return err
		}
	}
	return nil
}

func (t *Topology) buildLeafs() error {
	for pairIdx := 0; pairIdx < t.config.NumLeafPairs; pairIdx++ {
		leafASN := LeafASN(pairIdx)

		for leafNum := 1; leafNum <= 2; leafNum++ {
			name := fmt.Sprintf("leaf%d-as%d", leafNum, leafASN)
			routerID := LeafRouterID(pairIdx, leafNum)

			// Connect to ToRs
			for torIdx := 0; torIdx < t.config.NumToRsPerLeafPair; torIdx++ {
				globalToRIdx := pairIdx*t.config.NumToRsPerLeafPair + torIdx
				torASN := ToRASN(globalToRIdx)
				torName := fmt.Sprintf("tor%d-as%d", globalToRIdx, torASN)
				myIf := fmt.Sprintf("tr%d", torIdx)
				peerIf := fmt.Sprintf("lf%d", leafNum-1)

				t.addLink(name, myIf, leafASN, torName, peerIf, torASN)
			}

			var neighbors []Neighbor

			// Spine neighbors (peer info was set when spines were built)
			for spineIdx := 0; spineIdx < t.config.NumSpines; spineIdx++ {
				peerIf := fmt.Sprintf("sp%d", spineIdx)
				peerLLA, peerASN, localLLA := t.getPeerInfo(name, peerIf)

				neighbors = append(neighbors, Neighbor{
					Name:         fmt.Sprintf("spine%d", spineIdx),
					Interface:    peerIf,
					PeerASN:      peerASN,
					PeerLLA:      peerLLA,
					LocalLLA:     localLLA,
					ImportFilter: "leaf_import_from_spine",
					ExportFilter: "leaf_export_to_spine",
					MaxPrefix:    1000,
				})
			}

			// ToR neighbors
			for torIdx := 0; torIdx < t.config.NumToRsPerLeafPair; torIdx++ {
				globalToRIdx := pairIdx*t.config.NumToRsPerLeafPair + torIdx
				myIf := fmt.Sprintf("tr%d", torIdx)
				peerLLA, peerASN, localLLA := t.getPeerInfo(name, myIf)

				neighbors = append(neighbors, Neighbor{
					Name:         fmt.Sprintf("tor%d", globalToRIdx),
					Interface:    myIf,
					PeerASN:      peerASN,
					PeerLLA:      peerLLA,
					LocalLLA:     localLLA,
					ImportFilter: "leaf_import_from_tor",
					ExportFilter: "leaf_export_to_tor",
					MaxPrefix:    100,
				})
			}

			if err := t.addNodeConfig(name, routerID, "leaf", leafASN, neighbors, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *Topology) buildBorderLeafs() error {
	for blIdx := 0; blIdx < t.config.NumBorderLeafs; blIdx++ {
		name := fmt.Sprintf("bl%d", blIdx)
		routerID := BorderLeafRouterID(blIdx)

		// Connect to Routers
		for rtIdx := 0; rtIdx < t.config.NumRouters; rtIdx++ {
			rtName := fmt.Sprintf("router%d", rtIdx)
			myIf := fmt.Sprintf("rt%d", rtIdx)
			peerIf := fmt.Sprintf("bl%d", blIdx)

			t.addLink(name, myIf, ASNBorderLeaf, rtName, peerIf, ASNRouter)
		}

		var neighbors []Neighbor

		// Spine neighbors (peer info was set when spines were built)
		for spineIdx := 0; spineIdx < t.config.NumSpines; spineIdx++ {
			peerIf := fmt.Sprintf("sp%d", spineIdx)
			peerLLA, peerASN, localLLA := t.getPeerInfo(name, peerIf)

			neighbors = append(neighbors, Neighbor{
				Name:         fmt.Sprintf("spine%d", spineIdx),
				Interface:    peerIf,
				PeerASN:      peerASN,
				PeerLLA:      peerLLA,
				LocalLLA:     localLLA,
				ImportFilter: "bl_import_from_spine",
				ExportFilter: "bl_export_to_spine",
				MaxPrefix:    1000,
			})
		}

		// Router neighbors
		for rtIdx := 0; rtIdx < t.config.NumRouters; rtIdx++ {
			myIf := fmt.Sprintf("rt%d", rtIdx)
			peerLLA, peerASN, localLLA := t.getPeerInfo(name, myIf)

			neighbors = append(neighbors, Neighbor{
				Name:         fmt.Sprintf("router%d", rtIdx),
				Interface:    myIf,
				PeerASN:      peerASN,
				PeerLLA:      peerLLA,
				LocalLLA:     localLLA,
				ImportFilter: "bl_import_from_router",
				ExportFilter: "bl_export_to_router",
				MaxPrefix:    10,
			})
		}

		if err := t.addNodeConfig(name, routerID, "bl", ASNBorderLeaf, neighbors, false); err != nil {
			return err
		}
	}
	return nil
}

func (t *Topology) buildToRs() error {
	for pairIdx := 0; pairIdx < t.config.NumLeafPairs; pairIdx++ {
		for torIdx := 0; torIdx < t.config.NumToRsPerLeafPair; torIdx++ {
			globalToRIdx := pairIdx*t.config.NumToRsPerLeafPair + torIdx
			torASN := ToRASN(globalToRIdx)
			name := fmt.Sprintf("tor%d-as%d", globalToRIdx, torASN)
			routerID := ToRRouterID(globalToRIdx)

			// Connect to Servers
			for srvIdx := 0; srvIdx < t.config.NumServersPerToR; srvIdx++ {
				globalSrvIdx := globalToRIdx*t.config.NumServersPerToR + srvIdx
				srvASN := ServerASN(globalSrvIdx)
				srvName := fmt.Sprintf("server%d-as%d", globalSrvIdx, srvASN)
				myIf := fmt.Sprintf("sv%d", srvIdx)
				peerIf := "tr0"

				t.addLink(name, myIf, torASN, srvName, peerIf, srvASN)
			}

			var neighbors []Neighbor

			// Leaf neighbors (peer info was set when leafs were built)
			for leafNum := 1; leafNum <= 2; leafNum++ {
				peerIf := fmt.Sprintf("lf%d", leafNum-1)
				peerLLA, peerASN, localLLA := t.getPeerInfo(name, peerIf)

				neighbors = append(neighbors, Neighbor{
					Name:         fmt.Sprintf("leaf%d", leafNum),
					Interface:    peerIf,
					PeerASN:      peerASN,
					PeerLLA:      peerLLA,
					LocalLLA:     localLLA,
					ImportFilter: "tor_import_from_leaf",
					ExportFilter: "tor_export_to_leaf",
					MaxPrefix:    500,
				})
			}

			// Server neighbors
			for srvIdx := 0; srvIdx < t.config.NumServersPerToR; srvIdx++ {
				globalSrvIdx := globalToRIdx*t.config.NumServersPerToR + srvIdx
				myIf := fmt.Sprintf("sv%d", srvIdx)
				peerLLA, peerASN, localLLA := t.getPeerInfo(name, myIf)

				neighbors = append(neighbors, Neighbor{
					Name:         fmt.Sprintf("server%d", globalSrvIdx),
					Interface:    myIf,
					PeerASN:      peerASN,
					PeerLLA:      peerLLA,
					LocalLLA:     localLLA,
					ImportFilter: "tor_import_from_server",
					ExportFilter: "tor_export_to_server",
					MaxPrefix:    10,
				})
			}

			if err := t.addNodeConfig(name, routerID, "tor", torASN, neighbors, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *Topology) buildServers() error {
	serverNum := 0
	for pairIdx := 0; pairIdx < t.config.NumLeafPairs; pairIdx++ {
		for torIdx := 0; torIdx < t.config.NumToRsPerLeafPair; torIdx++ {
			globalToRIdx := pairIdx*t.config.NumToRsPerLeafPair + torIdx

			for srvIdx := 0; srvIdx < t.config.NumServersPerToR; srvIdx++ {
				serverASN := ServerASN(serverNum)
				name := fmt.Sprintf("server%d-as%d", serverNum, serverASN)
				routerID := ServerRouterID(serverNum)

				// ToR neighbor (peer info was set when ToRs were built)
				peerIf := "tr0"
				peerLLA, peerASN, localLLA := t.getPeerInfo(name, peerIf)

				neighbors := []Neighbor{{
					Name:         fmt.Sprintf("tor%d", globalToRIdx),
					Interface:    peerIf,
					PeerASN:      peerASN,
					PeerLLA:      peerLLA,
					LocalLLA:     localLLA,
					ImportFilter: "server_import",
					ExportFilter: "server_export",
					MaxPrefix:    100,
				}}

				if err := t.addNodeConfig(name, routerID, "server", serverASN, neighbors, true); err != nil {
					return err
				}
				serverNum++
			}
		}
	}
	return nil
}

func (t *Topology) buildRouters() error {
	for rtIdx := 0; rtIdx < t.config.NumRouters; rtIdx++ {
		name := fmt.Sprintf("router%d", rtIdx)
		routerID := RouterRouterID(rtIdx)
		var neighbors []Neighbor

		// Border Leaf neighbors (peer info was set when BLs were built)
		for blIdx := 0; blIdx < t.config.NumBorderLeafs; blIdx++ {
			peerIf := fmt.Sprintf("bl%d", blIdx)
			peerLLA, peerASN, localLLA := t.getPeerInfo(name, peerIf)

			neighbors = append(neighbors, Neighbor{
				Name:         fmt.Sprintf("bl%d", blIdx),
				Interface:    peerIf,
				PeerASN:      peerASN,
				PeerLLA:      peerLLA,
				LocalLLA:     localLLA,
				ImportFilter: "router_import",
				ExportFilter: "router_export",
				MaxPrefix:    1000,
			})
		}

		if err := t.addRouterNodeConfig(name, routerID, ASNRouter, neighbors, rtIdx); err != nil {
			return err
		}
	}
	return nil
}

// commonBirdExec returns the sequence of commands shared by every BIRD node:
// router-id on lo, MAC/LL setup, sysctls, /run/bird, then start BIRD.
// BIRD config is bind-mounted from cfg.BirdConfigDir/<name>.conf to /etc/bird/bird.conf,
// so no copy step is needed.
func (t *Topology) commonBirdExec(name string) []string {
	exec := append([]string{}, t.macCmds[name]...)
	exec = append(exec,
		"sysctl -w net.ipv4.ip_forward=1",
		"sysctl -w net.ipv6.conf.all.forwarding=1",
		"mkdir -p /run/bird",
		"bird -c /etc/bird/bird.conf",
	)
	return exec
}

// addRouterNodeConfig is like addNodeConfig but emits external-network commands
// (ext0 IP, default route, MASQUERADE) when ExternalNetwork is enabled.
func (t *Topology) addRouterNodeConfig(name, routerID string, asn int, neighbors []Neighbor, routerIndex int) error {
	birdConf, err := t.templates.Render("router", TemplateData{
		RouterID:  routerID,
		ASN:       asn,
		Neighbors: neighbors,
	})
	if err != nil {
		return fmt.Errorf("failed to render template for %s: %w", name, err)
	}
	t.birdConfigs[name] = birdConf

	exec := []string{fmt.Sprintf("ip addr add %s/32 dev lo", routerID)}
	exec = append(exec, t.commonBirdExec(name)...)

	if t.config.ExternalNetwork {
		externalIP := ExternalRouterIP(routerIndex)
		exec = append(exec,
			fmt.Sprintf("ip addr add %s/24 dev ext0", externalIP),
			fmt.Sprintf("ip route replace default via %s", ExternalNetworkGateway),
			"iptables -t nat -A POSTROUTING -o ext0 -j MASQUERADE",
		)
	}

	t.clabNodes[name] = ClabNode{
		Kind:  "linux",
		Image: ContainerImage,
		Binds: []string{
			fmt.Sprintf("%s/%s.conf:/etc/bird/bird.conf", t.config.BirdConfigDir, name),
		},
		Exec: exec,
	}
	t.nodeOrder = append(t.nodeOrder, name)
	return nil
}

// addNodeConfig renders the BIRD config for a non-router node and registers it as a clab node.
func (t *Topology) addNodeConfig(name, routerID, role string, asn int, neighbors []Neighbor, isServer bool) error {
	birdConf, err := t.templates.Render(role, TemplateData{
		RouterID:  routerID,
		ASN:       asn,
		Neighbors: neighbors,
	})
	if err != nil {
		return fmt.Errorf("failed to render template for %s: %w", name, err)
	}
	t.birdConfigs[name] = birdConf

	exec := []string{fmt.Sprintf("ip addr add %s/32 dev lo", routerID)}
	if isServer {
		exec = append(exec, fmt.Sprintf("ip addr add %s/32 dev lo", AnycastAddress))
	}
	exec = append(exec, t.commonBirdExec(name)...)

	t.clabNodes[name] = ClabNode{
		Kind:  "linux",
		Image: ContainerImage,
		Binds: []string{
			fmt.Sprintf("%s/%s.conf:/etc/bird/bird.conf", t.config.BirdConfigDir, name),
		},
		Exec: exec,
	}
	t.nodeOrder = append(t.nodeOrder, name)
	return nil
}

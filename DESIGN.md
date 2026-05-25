# closlab Design Document

This document describes the design philosophy and implementation details of closlab.

## Table of Contents

1. [Topology Design](#topology-design)
2. [ASN Design](#asn-design)
3. [Router ID Design](#router-id-design)
4. [BGP Unnumbered Implementation](#bgp-unnumbered-implementation)
5. [MAC Address and LLA Generation](#mac-address-and-lla-generation)
6. [BIRD Configuration Parameters](#bird-configuration-parameters)
7. [Filter Design](#filter-design)
8. [External Network Connectivity](#external-network-connectivity)

## Topology Design

### Clos Network

closlab generates a 5-tier Clos topology compliant with RFC 7938.

```
Router (External connectivity)
   |
Border Leaf (External/Internal boundary)
   |
Spine (Fabric core)
   |
Leaf (Aggregation)
   |
ToR (Top of Rack)
   |
Server (Endpoint)
```

### Role Descriptions

| Role        | Description                                                         |
|-------------|---------------------------------------------------------------------|
| Router      | Connection point to external networks. Injects default route        |
| Border Leaf | External/internal boundary. Connects Router and Spine               |
| Spine       | Fabric core. Connects to all Leaf/Border Leaf and aggregates routes |
| Leaf        | Aggregation layer. Connects Spine and ToR                           |
| ToR         | Server connectivity layer. Connects Leaf and Server                 |
| Server      | Endpoint. Advertises anycast address                                |

### Connection Patterns

- **Router - Border Leaf**: Each Router connects to all Border Leafs
- **Border Leaf - Spine**: Each Border Leaf connects to all Spines
- **Spine - Leaf**: Each Spine connects to all Leafs (full mesh)
- **Leaf - ToR**: Both Leafs in a pair connect to the same ToR group (redundancy)
- **ToR - Server**: Each ToR has 1:1 connections with its servers

## ASN Design

### Adoption of 4-byte ASN

closlab uses 4-byte ASN (RFC 6793). Large-scale data centers require more ASNs than the 2-byte private range (64512-65534) can provide, so 4-byte private ASN range (4200000000-4294967294) is used.

### ASN Assignment

| Role        | ASN                | Description                    |
|-------------|--------------------|--------------------------------|
| Spine       | 4200000000         | Shared across all Spines       |
| Border Leaf | 4200000001         | Shared across all Border Leafs |
| Router      | 4200000002         | Shared across all Routers      |
| Leaf        | 4200001000 + index | Unique per pair                |
| ToR         | 4200010000 + index | Unique per ToR                 |
| Server      | 4200100000 + index | Unique per Server              |

### Design Rationale

- **Spine/Border Leaf/Router share ASN**: No need for iBGP sessions between nodes in the same tier; the topology can be built with eBGP only
- **Leaf pairs share ASN**: Enables ECMP (same prefix advertised via different paths)
- **ToR/Server have individual ASN**: Uniquely identifies each node, facilitating troubleshooting

### ASN Range Separation

```
4200000000-4200000002  : Infrastructure tier (Spine, Border Leaf, Router)
4200001000-4200001999  : Leaf (up to 1000 pairs)
4200010000-4200019999  : ToR (up to 10000 units)
4200100000-4200199999  : Server (up to 100000 units)
```

Separating ranges allows immediate role identification from ASN.

## Router ID Design

### Address Space

| Role        | Router ID Range               | Maximum |
|-------------|-------------------------------|---------|
| Spine       | 10.255.0.1 - 10.255.0.254     | 254     |
| Leaf        | 10.255.1.0 - 10.255.1.255     | 256     |
| ToR         | 10.255.2.1 - 10.255.3.254     | 510     |
| Border Leaf | 10.255.254.1 - 10.255.254.254 | 254     |
| Router      | 10.255.255.1 - 10.255.255.254 | 254     |
| Server      | 10.0.0.1 - 10.0.255.254       | 65534   |

### Design Rationale

- **10.255.0.0/16**: Infrastructure devices (Spine, Leaf, ToR, Border Leaf, Router)
- **10.0.0.0/16**: Servers (large range allocated)
- **10.100.0.0/24**: Anycast

Router ID is configured on the loopback interface and used as BGP identifier.

## BGP Unnumbered Implementation

### Overview

BGP Unnumbered establishes BGP sessions without configuring IPv4 addresses on interfaces. It uses IPv6 link-local addresses (LLA) to establish BGP sessions and advertise IPv4 prefixes (RFC 5549/8950).

### Approach

BIRD does not have FRRouting's `neighbor <interface> interface` syntax and requires explicit IPv6 LLA specification. closlab addresses this as follows:

1. **Generate MAC addresses deterministically**
2. **Calculate LLA from MAC using EUI-64 algorithm**
3. **Configure MAC and LLA in node_configs**
4. **Embed pre-calculated LLA in BIRD configuration**

### Necessity of local Directive

Since the generated LLA is added alongside the kernel-generated LLA, the source LLA must be explicitly specified using the `local` directive:

```
protocol bgp spine0 {
    neighbor fe80::ff:fe00:0%sp0 as 4200000000;
    local fe80::ff:fe00:100 as 4200001000;
    direct;
    ...
}
```

## MAC Address and LLA Generation

### MAC Address Generation

closlab assigns a unique MAC address to each link:

```
02:XX:XX:XX:XX:00
|  |__________|__ linkID (32bit)
|__ Locally Administered (U/L bit = 1)
```

- Bit 1 of the first byte (U/L bit) is set to 1, indicating a locally administered address
- Uniqueness is guaranteed by incrementing linkID

### EUI-64 Conversion

MAC to IPv6 LLA conversion follows RFC 4291 Section 2.5.1:

```
MAC:    02:00:00:00:01:00
        |              |
        v              v
        02:00:00 : FF:FE : 00:01:00  <- Insert FF:FE in the middle
        |
        v
        00:00:00:FF:FE:00:01:00      <- Invert U/L bit (02 -> 00)
        |
        v
LLA:    fe80::ff:fe00:100
```

## BIRD Configuration Parameters

### direct

```
protocol bgp {
    direct;
    ...
}
```

`direct` indicates that the BGP neighbor is directly connected. It tells BIRD this is a BGP session over a directly attached link, not multihop BGP.

### extended next hop

```
ipv4 {
    extended next hop;
    ...
}
```

Enables RFC 5549/8950 Extended Next Hop Encoding. This allows using IPv6 addresses (LLA) as next hops for IPv4 prefixes.

### bfd on

```
protocol bgp {
    bfd on;
    ...
}
```

Enables Bidirectional Forwarding Detection (BFD). BFD detects link failures faster than BGP (millisecond order), reducing BGP convergence time.

BFD parameters:
```
protocol bfd {
    interface "*" {
        min rx interval 100 ms;
        min tx interval 100 ms;
        idle tx interval 1000 ms;
        multiplier 3;
    };
}
```

### graceful restart on

```
protocol bgp {
    graceful restart on;
    ...
}
```

Enables BGP Graceful Restart (RFC 4724). Temporarily maintains sessions during BIRD process restart, preventing route flapping.

### receive limit

```
ipv4 {
    receive limit 500 action warn;
    ...
}
```

Sets the maximum number of prefixes to receive from a neighbor. The `action` specifies behavior when the limit is exceeded.

closlab uses `action warn` to preserve the problematic state for investigation without disrupting the session.

### merge paths

```
protocol kernel {
    merge paths;
    ...
}
```

Injects multiple routes to the same prefix into the kernel as multipath (ECMP). This distributes traffic across multiple paths.

## Filter Design

### Role of Filters

Each role has Import/Export filters that control which prefixes are received/advertised. This prevents unintended route propagation and ensures routing stability.

### Prefix Classification

| Prefix        | Purpose                             |
|---------------|-------------------------------------|
| 10.255.0.0/16 | Infrastructure loopback (Router ID) |
| 10.0.0.0/16   | Server loopback                     |
| 10.100.0.0/24 | Anycast address                     |
| 0.0.0.0/0     | Default route                       |

### Filter List

#### Spine

| Filter       | Allowed Prefixes                                     |
|--------------|------------------------------------------------------|
| spine_import | 10.255.0.0/16, 10.0.0.0/16, 10.100.0.0/24, 0.0.0.0/0 |
| spine_export | 10.255.0.0/16, 10.0.0.0/16, 10.100.0.0/24, 0.0.0.0/0 |

#### Leaf

| Filter                 | Allowed Prefixes                                         |
|------------------------|----------------------------------------------------------|
| leaf_import_from_spine | 10.255.0.0/16, 10.0.0.0/16, 10.100.0.0/24, 0.0.0.0/0     |
| leaf_import_from_tor   | 10.255.2.0/23, 10.0.0.0/16, 10.100.0.0/24                |
| leaf_export_to_spine   | 10.255.1.0/24, 10.255.2.0/23, 10.0.0.0/16, 10.100.0.0/24 |
| leaf_export_to_tor     | 10.255.0.0/16, 10.0.0.0/16, 10.100.0.0/24, 0.0.0.0/0     |

#### Border Leaf

| Filter                | Allowed Prefixes                            |
|-----------------------|---------------------------------------------|
| bl_import_from_spine  | 10.255.0.0/16, 10.0.0.0/16, 10.100.0.0/24   |
| bl_import_from_router | 10.255.255.0/24, 0.0.0.0/0                  |
| bl_export_to_spine    | 10.255.254.0/24, 10.255.255.0/24, 0.0.0.0/0 |
| bl_export_to_router   | 10.255.0.0/16, 10.0.0.0/16, 10.100.0.0/24   |

#### ToR

| Filter                 | Allowed Prefixes                                     |
|------------------------|------------------------------------------------------|
| tor_import_from_leaf   | 10.255.0.0/16, 10.0.0.0/16, 10.100.0.0/24, 0.0.0.0/0 |
| tor_import_from_server | 10.0.0.0/16, 10.100.0.0/24                           |
| tor_export_to_leaf     | 10.255.2.0/23, 10.0.0.0/16, 10.100.0.0/24            |
| tor_export_to_server   | 10.255.0.0/16, 10.0.0.0/16, 10.100.0.0/24, 0.0.0.0/0 |

#### Server

| Filter        | Allowed Prefixes                                     |
|---------------|------------------------------------------------------|
| server_import | 10.255.0.0/16, 10.0.0.0/16, 10.100.0.0/24, 0.0.0.0/0 |
| server_export | 10.0.0.0/16, 10.100.0.0/24                           |

#### Router

| Filter        | Allowed Prefixes                          |
|---------------|-------------------------------------------|
| router_import | 10.255.0.0/16, 10.0.0.0/16, 10.100.0.0/24 |
| router_export | 10.255.255.0/24, 0.0.0.0/0                |

### Default Route Propagation

The default route (0.0.0.0/0) propagates through the following path:

```
Router (generated by: protocol static)
   | router_export
   v
Border Leaf
   | bl_export_to_spine
   v
Spine
   | spine_export
   v
Leaf
   | leaf_export_to_tor
   v
ToR
   | tor_export_to_server
   v
Server
```

## External Network Connectivity

### Overview

The `-external-network` option enables internet connectivity from nodes within the Clos network. This feature is designed for learning environments to verify end-to-end connectivity.

### Architecture

```
Server -> ToR -> Leaf -> Spine -> Border Leaf -> Router -> ext (OVS) -> Host -> Internet
                                                   |
                                              MASQUERADE
```

### Network Design

| Item                    | Value              |
|-------------------------|--------------------|
| Subnet                  | 172.31.255.0/24    |
| Gateway (host side)     | 172.31.255.1       |
| router0                 | 172.31.255.2       |
| router1                 | 172.31.255.3       |
| router N                | 172.31.255.(N+2)   |

### How It Works

1. All routers connect to the OVS bridge `ext` via `eth0`
2. Each router executes `iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE`
3. Internal Clos addresses (10.0.0.0/8, etc.) are NAT-translated to the router's external address (172.31.255.X)
4. The host performs MASQUERADE for 172.31.255.0/24 to the external interface

### Relationship with BIRD Configuration

The `route 0.0.0.0/0 blackhole` in the router's BIRD configuration is for BGP advertisement only and is not used for actual packet forwarding. Actual forwarding uses the default route via `eth0` (`ip route add default via 172.31.255.1`).

### Limitations

- Open vSwitch installation is required
- Host-side configuration is required (IP address, iptables rules, IP forwarding)
- Internet connectivity is implemented with NAT, which differs from production environments

## References

- [RFC 4291 - IP Version 6 Addressing Architecture](https://datatracker.ietf.org/doc/html/rfc4291)
- [RFC 4724 - Graceful Restart Mechanism for BGP](https://datatracker.ietf.org/doc/html/rfc4724)
- [RFC 5549 - Advertising IPv4 Network Layer Reachability Information with an IPv6 Next Hop](https://datatracker.ietf.org/doc/html/rfc5549)
- [RFC 6793 - BGP Support for Four-Octet Autonomous System (AS) Number Space](https://datatracker.ietf.org/doc/html/rfc6793)
- [RFC 7938 - Use of BGP for Routing in Large-Scale Data Centers](https://datatracker.ietf.org/doc/html/rfc7938)
- [RFC 8950 - Advertising IPv4 Network Layer Reachability Information (NLRI) with an IPv6 Next Hop](https://datatracker.ietf.org/doc/html/rfc8950)
- [BIRD 3.2 User's Guide - BGP](https://bird.nic.cz/doc/bird-3.2.1.html#bgp)

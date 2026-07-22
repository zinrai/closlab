package main

import (
	"flag"
	"os"
)

// Config holds the topology configuration.
type Config struct {
	NumSpines          int
	NumLeafPairs       int
	NumToRsPerLeafPair int
	NumServersPerToR   int
	NumBorderLeafs     int
	NumRouters         int

	BirdConfigDir     string
	BirdTemplates     string
	ExternalNetwork   bool
	ExternalInterface string
}

// DefaultConfig returns the default configuration (small for testing).
func DefaultConfig() Config {
	return Config{
		NumSpines:          2,
		NumLeafPairs:       1,
		NumToRsPerLeafPair: 2,
		NumServersPerToR:   2,
		NumBorderLeafs:     1,
		NumRouters:         1,
		BirdConfigDir:      "./output",
		BirdTemplates:      "templates.yaml",
		ExternalNetwork:    false,
		ExternalInterface:  "",
	}
}

// ParseFlags parses command line flags and returns a Config.
func ParseFlags() Config {
	cfg := DefaultConfig()

	flag.IntVar(&cfg.NumSpines, "spines", cfg.NumSpines, "Number of spine switches")
	flag.IntVar(&cfg.NumLeafPairs, "leaf-pairs", cfg.NumLeafPairs, "Number of leaf switch pairs")
	flag.IntVar(&cfg.NumToRsPerLeafPair, "tors-per-pair", cfg.NumToRsPerLeafPair, "Number of ToR switches per leaf pair")
	flag.IntVar(&cfg.NumServersPerToR, "servers-per-tor", cfg.NumServersPerToR, "Number of servers per ToR switch")
	flag.IntVar(&cfg.NumBorderLeafs, "border-leaves", cfg.NumBorderLeafs, "Number of border leaf switches")
	flag.IntVar(&cfg.NumRouters, "routers", cfg.NumRouters, "Number of external routers")
	flag.StringVar(&cfg.BirdConfigDir, "bird-config-dir", cfg.BirdConfigDir, "Directory to output BIRD configuration files")
	flag.StringVar(&cfg.BirdTemplates, "bird-templates", cfg.BirdTemplates, "Path to BIRD templates YAML file")
	flag.BoolVar(&cfg.ExternalNetwork, "external-network", cfg.ExternalNetwork, "Enable external network connectivity via host Linux bridge")
	flag.StringVar(&cfg.ExternalInterface, "external-interface", cfg.ExternalInterface, "Host interface for external network (required with -external-network)")

	showVersion := flag.Bool("version", false, "Print version information and exit")

	flag.Parse()

	if *showVersion {
		printVersion()
		os.Exit(0)
	}

	return cfg
}

// TotalNodes returns the total number of nodes in the topology.
func (c Config) TotalNodes() int {
	leafs := c.NumLeafPairs * 2
	tors := c.NumLeafPairs * c.NumToRsPerLeafPair
	servers := tors * c.NumServersPerToR
	return c.NumSpines + leafs + c.NumBorderLeafs + tors + servers + c.NumRouters
}

// TotalToRs returns the total number of ToRs in the topology.
func (c Config) TotalToRs() int {
	return c.NumLeafPairs * c.NumToRsPerLeafPair
}

// TotalServers returns the total number of servers in the topology.
func (c Config) TotalServers() int {
	return c.TotalToRs() * c.NumServersPerToR
}

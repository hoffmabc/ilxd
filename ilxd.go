// Copyright (c) 2024 Project Illium
// Use of this source code is governed by an MIT
// license that can be found in the LICENSE file.

package main

import (
	"github.com/jessevdk/go-flags"
	"github.com/project-illium/ilxd/limits"
	"github.com/project-illium/ilxd/repo"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Up some limits.
	if err := limits.SetLimits(); err != nil {
		log.WithCaller(true).Fatal("Failed to set limits", log.Args("error", err))
	}

	// Configure the command line parser.
	var emptyCfg repo.Config
	parser := flags.NewNamedParser("ilxd", flags.Default)
	parser.AddGroup("Node Options", "Configuration options for the node", &emptyCfg)
	if _, err := parser.Parse(); err != nil {
		log.WithCaller(true).Fatal("Failed to configure parser", log.Args("error", err))
		return
	}

	// Load the config file. There are three steps to this:
	// 1. Start with a config populated with default values.
	// 2. Override the default values with any provided config file options.
	// 3. Override the first two with any provided command line options.
	cfg, err := repo.LoadConfig()
	if err != nil {
		log.WithCaller(true).Fatal("Failed to load config", log.Args("error", err))
	}

	// Build and start the server.
	server, err := BuildServer(cfg)
	if err != nil {
		log.WithCaller(true).Fatal("Failed to build server", log.Args("error", err))
	}

	// Listen for an exit signal and close.
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	for sig := range c {
		if sig == syscall.SIGINT || sig == syscall.SIGTERM {
			log.Info("ilxd gracefully shutting down")
			if err := server.Close(); err != nil {
				log.WithCaller(true).Error("Shutdown error", log.Args("error", err))
			}
			os.Exit(1)
		}
	}
}

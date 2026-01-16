package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	
	"github.com/grovetools/tend/pkg/app"
	"github.com/grovetools/grove/tests"
)

func main() {
	// Get all E2E scenarios for grove-meta
	scenarios := tests.AllScenarios()
	
	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nReceived interrupt signal, shutting down...")
		cancel()
	}()
	
	// Execute the custom tend application with our scenarios
	if err := app.Execute(ctx, scenarios); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
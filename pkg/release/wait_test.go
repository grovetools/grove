package release

import (
	"context"
	"testing"
	"time"
)

func TestWaitConfig(t *testing.T) {
	config := DefaultWaitConfig()
	
	if config.MaxRetries != 20 {
		t.Errorf("Expected MaxRetries to be 20, got %d", config.MaxRetries)
	}
	
	if config.InitialBackoff != 15*time.Second {
		t.Errorf("Expected InitialBackoff to be 15s, got %v", config.InitialBackoff)
	}
	
	if config.MaxBackoff != 60*time.Second {
		t.Errorf("Expected MaxBackoff to be 60s, got %v", config.MaxBackoff)
	}
	
	if config.Timeout != 5*time.Minute {
		t.Errorf("Expected Timeout to be 5m, got %v", config.Timeout)
	}
}

func TestWaitForModuleAvailabilityTimeout(t *testing.T) {
	ctx := context.Background()
	config := WaitConfig{
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     20 * time.Millisecond,
		Timeout:        100 * time.Millisecond,
	}
	
	// This should timeout since the module doesn't exist
	err := WaitForModuleAvailabilityWithConfig(ctx, "fake.module/test", "v1.0.0", config)
	if err == nil {
		t.Error("Expected error for non-existent module, got nil")
	}
}
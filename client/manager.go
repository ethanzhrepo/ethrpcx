package client

import (
	"log"
	"sync"
	"time"
)

// ClientManager manages a pool of RPC clients
type ClientManager struct {
	clients     map[string]*Client
	config      Config
	mu          sync.RWMutex
	cleanupDur  time.Duration
	idleTimeout time.Duration
	stopChan    chan struct{}
}

// ClientManagerConfig holds configuration for ClientManager
type ClientManagerConfig struct {
	// Basic client configuration
	ClientConfig Config

	// How often to run the cleanup process
	CleanupInterval time.Duration

	// How long an endpoint can be idle before closing
	IdleTimeout time.Duration
}

// DefaultClientManagerConfig provides default values for ClientManagerConfig
var DefaultClientManagerConfig = ClientManagerConfig{
	ClientConfig:    DefaultConfig,
	CleanupInterval: 5 * time.Minute,
	IdleTimeout:     30 * time.Minute,
}

// NewClientManager creates a new client manager with the given config
func NewClientManager(config Config) *ClientManager {
	return NewClientManagerWithConfig(ClientManagerConfig{
		ClientConfig:    config,
		CleanupInterval: DefaultClientManagerConfig.CleanupInterval,
		IdleTimeout:     DefaultClientManagerConfig.IdleTimeout,
	})
}

// NewClientManagerWithConfig creates a new client manager with detailed configuration
func NewClientManagerWithConfig(config ClientManagerConfig) *ClientManager {
	// Set default values if not provided
	clientConfig := config.ClientConfig
	if clientConfig.Timeout == 0 {
		clientConfig.Timeout = DefaultConfig.Timeout
	}
	if clientConfig.ConnectionTimeout == 0 {
		clientConfig.ConnectionTimeout = DefaultConfig.ConnectionTimeout
	}
	if clientConfig.MaxConnectionRetries == 0 {
		clientConfig.MaxConnectionRetries = DefaultConfig.MaxConnectionRetries
	}
	if clientConfig.RetryDelay == 0 {
		clientConfig.RetryDelay = DefaultConfig.RetryDelay
	}
	if clientConfig.MaxRetryDelay == 0 {
		clientConfig.MaxRetryDelay = DefaultConfig.MaxRetryDelay
	}

	cleanupDur := config.CleanupInterval
	if cleanupDur == 0 {
		cleanupDur = DefaultClientManagerConfig.CleanupInterval
	}

	idleTimeout := config.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = DefaultClientManagerConfig.IdleTimeout
	}

	cm := &ClientManager{
		clients:     make(map[string]*Client),
		config:      clientConfig,
		cleanupDur:  cleanupDur,
		idleTimeout: idleTimeout,
		stopChan:    make(chan struct{}),
	}

	go cm.periodicCleanup()
	return cm
}

// periodicCleanup closes idle connections periodically
func (cm *ClientManager) periodicCleanup() {
	ticker := time.NewTicker(cm.cleanupDur)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cm.cleanupIdleConnections()
		case <-cm.stopChan:
			return
		}
	}
}

// cleanupIdleConnections closes idle connections
func (cm *ClientManager) cleanupIdleConnections() {
	now := time.Now()

	cm.mu.Lock()
	defer cm.mu.Unlock()

	for id, client := range cm.clients {
		client.mu.RLock()

		// Check if any endpoints are still active and not idle
		allIdle := true
		for _, endpoint := range client.endpoints {
			endpoint.mu.RLock()
			if !endpoint.IsClosed && now.Sub(endpoint.LastUsed) <= cm.idleTimeout {
				allIdle = false
			}
			endpoint.mu.RUnlock()

			if !allIdle {
				break
			}
		}

		client.mu.RUnlock()

		if allIdle {
			log.Printf("Closing idle client: %s (idle for more than %v)", id, cm.idleTimeout)
			client.Close()
			delete(cm.clients, id)
		}
	}
}

// GetClient returns a client for the specified configuration key
func (cm *ClientManager) GetClient(key string, endpoints []string) (*Client, error) {
	cm.mu.RLock()
	client, exists := cm.clients[key]
	cm.mu.RUnlock()

	// If client exists and is active, return it
	if exists {
		// Check if any endpoints are still active
		client.mu.RLock()
		hasActiveEndpoint := false
		for _, endpoint := range client.endpoints {
			endpoint.mu.RLock()
			if !endpoint.IsClosed {
				hasActiveEndpoint = true
			}
			endpoint.mu.RUnlock()

			if hasActiveEndpoint {
				break
			}
		}
		client.mu.RUnlock()

		if hasActiveEndpoint {
			return client, nil
		}
	}

	// Need to create or reconnect a client
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Double-check after acquiring write lock
	client, exists = cm.clients[key]
	if exists {
		// Check if any endpoints are still active
		client.mu.RLock()
		hasActiveEndpoint := false
		for _, endpoint := range client.endpoints {
			endpoint.mu.RLock()
			if !endpoint.IsClosed {
				hasActiveEndpoint = true
			}
			endpoint.mu.RUnlock()

			if hasActiveEndpoint {
				break
			}
		}
		client.mu.RUnlock()

		if hasActiveEndpoint {
			return client, nil
		}
	}

	// Create a new client configuration
	clientConfig := cm.config
	clientConfig.Endpoints = endpoints

	// Create a new client
	newClient, err := NewClient(clientConfig)
	if err != nil {
		return nil, err
	}

	cm.clients[key] = newClient
	return newClient, nil
}

// CloseClient closes a specific client
func (cm *ClientManager) CloseClient(key string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if client, exists := cm.clients[key]; exists {
		client.Close()
		delete(cm.clients, key)
	}
}

// CloseAll closes all clients
func (cm *ClientManager) CloseAll() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	close(cm.stopChan)

	for key, client := range cm.clients {
		client.Close()
		delete(cm.clients, key)
	}
}

// Global client manager instance
var (
	globalClientManager *ClientManager
	clientManagerOnce   sync.Once
)

// GetGlobalClientManager returns the global client manager instance
func GetGlobalClientManager() *ClientManager {
	clientManagerOnce.Do(func() {
		globalClientManager = NewClientManagerWithConfig(DefaultClientManagerConfig)
		log.Printf("Initialized global RPC client manager")
	})
	return globalClientManager
}

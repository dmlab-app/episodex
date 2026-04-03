// Package tracker provides a modular tracker interface for checking and downloading torrents.
package tracker

import "fmt"

// Client is the interface that all tracker implementations must satisfy.
// Each tracker (Kinozal, Rutracker, etc.) implements this interface.
type Client interface {
	// CanHandle returns true if this client handles the given tracker URL.
	CanHandle(trackerURL string) bool
	// GetEpisodeCount fetches the torrent page and returns the number of episodes available.
	GetEpisodeCount(trackerURL string) (int, error)
	// DownloadTorrent downloads the .torrent file by tracker URL, returns raw bytes.
	DownloadTorrent(trackerURL string) ([]byte, error)
}

// Registry holds multiple Client implementations and routes URLs to the right one.
type Registry struct {
	clients []Client
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a Client to the registry.
func (r *Registry) Register(client Client) {
	r.clients = append(r.clients, client)
}

// Clients returns the list of registered clients.
func (r *Registry) Clients() []Client {
	return r.clients
}

// GetClient returns the Client that can handle the given URL.
// Returns an error if no client can handle the URL.
func (r *Registry) GetClient(trackerURL string) (Client, error) {
	for _, c := range r.clients {
		if c.CanHandle(trackerURL) {
			return c, nil
		}
	}
	return nil, fmt.Errorf("no tracker client found for URL: %s", trackerURL)
}

package tracker

import "fmt"

// TrackerClient is the interface that all tracker implementations must satisfy.
// Each tracker (Kinozal, Rutracker, etc.) implements this interface.
type TrackerClient interface {
	// CanHandle returns true if this client handles the given tracker URL.
	CanHandle(trackerURL string) bool
	// GetEpisodeCount fetches the torrent page and returns the number of episodes available.
	GetEpisodeCount(trackerURL string) (int, error)
	// DownloadTorrent downloads the .torrent file by tracker URL, returns raw bytes.
	DownloadTorrent(trackerURL string) ([]byte, error)
}

// Registry holds multiple TrackerClient implementations and routes URLs to the right one.
type Registry struct {
	clients []TrackerClient
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a TrackerClient to the registry.
func (r *Registry) Register(client TrackerClient) {
	r.clients = append(r.clients, client)
}

// GetClient returns the TrackerClient that can handle the given URL.
// Returns an error if no client can handle the URL.
func (r *Registry) GetClient(trackerURL string) (TrackerClient, error) {
	for _, c := range r.clients {
		if c.CanHandle(trackerURL) {
			return c, nil
		}
	}
	return nil, fmt.Errorf("no tracker client found for URL: %s", trackerURL)
}

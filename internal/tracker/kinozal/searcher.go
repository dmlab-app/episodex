package kinozal

import "github.com/episodex/episodex/internal/tracker"

// SeasonSearcher wraps Client to implement tracker.SeasonSearcher.
func (c *Client) SeasonSearcher() tracker.SeasonSearcher {
	return &kinozalSeasonSearcher{client: c}
}

type kinozalSeasonSearcher struct {
	client *Client
}

func (s *kinozalSeasonSearcher) FindSeasonTorrent(query string, seasonNumber int) (*tracker.SeasonSearchResult, error) {
	result, err := s.client.FindSeasonTorrent(query, seasonNumber)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return &tracker.SeasonSearchResult{
		Title:      result.Title,
		Size:       result.Size,
		DetailsURL: result.DetailsURL,
	}, nil
}

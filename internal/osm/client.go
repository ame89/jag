package osm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// ErrNotFound is returned when Nominatim yields no result for the given
// address/query.
var ErrNotFound = errors.New("osm: address not found")

// Client geocodes addresses via a Nominatim instance. It is safe for
// concurrent use; requests are serialized internally to honor
// Config.MinRequestInterval (client-side rate limiting).
type Client struct {
	cfg        Config
	httpClient *http.Client

	mu          sync.Mutex
	lastRequest time.Time
}

// NewClient creates a Client from cfg. If cfg.BaseURL is empty, the public
// Nominatim instance is used. cfg.UserAgent should be set to a value
// identifying the calling application (required by Nominatim's usage
// policy) — requests are still sent without one, but the public instance
// may reject/block them.
func NewClient(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultConfig().BaseURL
	}
	if cfg.MinRequestInterval == 0 {
		cfg.MinRequestInterval = DefaultConfig().MinRequestInterval
	}
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Geocode looks up WGS84 coordinates for a free-form address string (e.g.
// "Musterstraße 1, 12345 Musterstadt, Germany") and returns the best match.
// Returns ErrNotFound if Nominatim has no match.
func (c *Client) Geocode(ctx context.Context, address string) (Coordinate, error) {
	return c.search(ctx, url.Values{"q": {address}})
}

// GeocodeStructured looks up WGS84 coordinates using Nominatim's structured
// query parameters instead of a single free-form string, which tends to give
// more reliable matches when the individual address components are already
// known separately. Any empty field is omitted from the query.
func (c *Client) GeocodeStructured(ctx context.Context, street, houseNumber, postcode, city, country string) (Coordinate, error) {
	q := url.Values{}
	// Nominatim's "street" parameter expects house number and street combined.
	street = combineStreetAndHouseNumber(street, houseNumber)
	if street != "" {
		q.Set("street", street)
	}
	if postcode != "" {
		q.Set("postalcode", postcode)
	}
	if city != "" {
		q.Set("city", city)
	}
	if country != "" {
		q.Set("country", country)
	}
	if len(q) == 0 {
		return Coordinate{}, fmt.Errorf("osm: GeocodeStructured: at least one address field must be set")
	}
	return c.search(ctx, q)
}

func combineStreetAndHouseNumber(street, houseNumber string) string {
	switch {
	case street == "":
		return ""
	case houseNumber == "":
		return street
	default:
		return street + " " + houseNumber
	}
}

func (c *Client) search(ctx context.Context, params url.Values) (Coordinate, error) {
	c.throttle()

	params.Set("format", "json")
	params.Set("limit", "1")

	reqURL := c.cfg.BaseURL + "/search?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return Coordinate{}, fmt.Errorf("osm: building request: %w", err)
	}
	if c.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", c.cfg.UserAgent)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Coordinate{}, fmt.Errorf("osm: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Coordinate{}, fmt.Errorf("osm: unexpected status %d from %s", resp.StatusCode, reqURL)
	}

	var results []nominatimResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return Coordinate{}, fmt.Errorf("osm: decoding response: %w", err)
	}
	if len(results) == 0 {
		return Coordinate{}, ErrNotFound
	}

	lat, err := strconv.ParseFloat(results[0].Lat, 64)
	if err != nil {
		return Coordinate{}, fmt.Errorf("osm: parsing lat %q: %w", results[0].Lat, err)
	}
	lon, err := strconv.ParseFloat(results[0].Lon, 64)
	if err != nil {
		return Coordinate{}, fmt.Errorf("osm: parsing lon %q: %w", results[0].Lon, err)
	}
	return Coordinate{Lat: lat, Lon: lon}, nil
}

// throttle blocks as needed to keep at least Config.MinRequestInterval
// between outgoing requests, per Nominatim's usage policy.
func (c *Client) throttle() {
	c.mu.Lock()
	defer c.mu.Unlock()

	minInterval := time.Duration(c.cfg.MinRequestInterval) * time.Millisecond
	if wait := minInterval - time.Since(c.lastRequest); wait > 0 {
		time.Sleep(wait)
	}
	c.lastRequest = time.Now()
}

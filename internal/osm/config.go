// Package osm provides a small client for geocoding addresses (turning a
// postal address into WGS84 coordinates) by querying OpenStreetMap's public
// Nominatim search API (https://nominatim.org/release-docs/latest/api/Search/).
//
// Nominatim (not the Overpass API, which is used elsewhere for pulling grid
// infrastructure data, see spec/OSM-Anwendungsfall.md) is OSM's dedicated
// geocoding service and the correct tool for "address -> coordinate" lookups.
package osm

// Config configures the Nominatim client.
type Config struct {
	// BaseURL is the Nominatim search endpoint base, e.g.
	// "https://nominatim.openstreetmap.org". Defaults to the public
	// OSM-hosted instance if empty.
	BaseURL string

	// UserAgent identifies the calling application, as required by the
	// Nominatim usage policy (https://operations.osmfoundation.org/policies/nominatim/):
	// requests without a distinguishing User-Agent/Referer may be blocked.
	// Must be set to something meaningful (e.g. "jag/0.1 (contact@example.com)").
	UserAgent string

	// MinRequestInterval enforces client-side rate limiting between
	// requests. The public Nominatim policy caps usage at 1 request/second;
	// defaults to 1 second if zero.
	MinRequestInterval int64 // milliseconds
}

// DefaultConfig returns a Config pointing at the public Nominatim instance
// with the policy-compliant default rate limit (1 request/second). Callers
// MUST still set UserAgent themselves before using the client.
func DefaultConfig() Config {
	return Config{
		BaseURL:            "https://nominatim.openstreetmap.org",
		MinRequestInterval: 1000,
	}
}

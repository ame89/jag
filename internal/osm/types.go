package osm

import "gitlab.com/openk-nsc/jag/internal/geo"

// Coordinate is a WGS84 geo-coordinate. Aliased from internal/geo so callers
// that also need geo.DistanceMeters (e.g. between a geocoded address and a
// grid element's Geometry) can use the two packages interchangeably.
type Coordinate = geo.Coordinate

// nominatimResult mirrors the fields we need from a single element of
// Nominatim's /search JSON array response. Nominatim returns lat/lon as
// strings, not numbers.
type nominatimResult struct {
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	DisplayName string `json:"display_name"`
}

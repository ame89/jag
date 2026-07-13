// Package geo provides small, dependency-light geo-coordinate utilities
// (WGS84 point type, geodesic distance) shared across JAG's Geometry-related
// code (see spec/Konzept.md, "Geometrie"). This package holds pure
// data/utility logic only — it doesn't know about Equipment/Container
// ownership or storage, unlike internal/core/geometry.
package geo

import "github.com/tidwall/geodesic"

// Coordinate is a WGS84 geo-coordinate (2D only — no height/depth,
// consistent with JAG's Geometry model).
type Coordinate struct {
	Lat float64
	Lon float64
}

// DistanceMeters returns the geodesic (ellipsoidal, WGS84) distance between
// a and b in meters, using Karney's algorithm via github.com/tidwall/geodesic.
func DistanceMeters(a, b Coordinate) float64 {
	var s12 float64
	geodesic.WGS84.Inverse(a.Lat, a.Lon, b.Lat, b.Lon, &s12, nil, nil)
	return s12
}

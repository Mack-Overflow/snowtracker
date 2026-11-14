// Package terrain decides which elevations to forecast for a place.
package terrain

import (
	"math"
	"strings"

	"precip-calendar/api/internal/compute"
)

// minRelief is the vertical spread (m) below which a single band is enough.
const minRelief = 400

var ringRadiiKm = []float64{2, 4, 6, 8, 10}

const ringBearings = 16

// Ring returns sample coordinates around a point: the point itself first,
// then 16 bearings at each radius.
func Ring(lat, lon float64) (lats, lons []float64) {
	lats, lons = []float64{lat}, []float64{lon}
	for _, km := range ringRadiiKm {
		for i := 0; i < ringBearings; i++ {
			bearing := float64(i) * 2 * math.Pi / ringBearings
			lats = append(lats, lat+km/111.0*math.Cos(bearing))
			lons = append(lons, lon+km/(111.0*math.Cos(lat*math.Pi/180))*math.Sin(bearing))
		}
	}
	return lats, lons
}

func isPeakKind(kind string) bool {
	switch strings.ToUpper(kind) {
	case "MT", "MTS", "PK", "PKS", "VLC", "RDGE", "HLL", "HLLS", "GLCR", "PASS":
		return true
	}
	return false
}

// Bands builds top-to-bottom elevation bands. pointElev is the place itself;
// around holds terrain heights sampled near it (may be empty).
//
// A peak gets Summit / Mid-mountain / Base, with the base taken from the
// lowest nearby terrain. A town gets itself plus the high ground above it.
// Flat places get a single band.
func Bands(kind string, pointElev float64, around []float64) []compute.Band {
	lo, hi := pointElev, pointElev
	for _, e := range around {
		lo, hi = math.Min(lo, e), math.Max(hi, e)
	}
	peak := isPeakKind(kind) || (len(around) > 0 && pointElev >= hi-50 && hi-lo >= minRelief)

	if peak {
		if pointElev-lo < minRelief {
			return []compute.Band{{ID: "point", Label: "Summit", Elevation: pointElev}}
		}
		return []compute.Band{
			{ID: "summit", Label: "Summit", Elevation: pointElev},
			{ID: "mid", Label: "Mid-mountain", Elevation: round10((pointElev + lo) / 2), Modelled: true},
			{ID: "base", Label: "Base", Elevation: round10(lo), Modelled: true},
		}
	}
	if hi-pointElev < minRelief {
		return []compute.Band{{ID: "point", Label: "Town", Elevation: pointElev}}
	}
	return []compute.Band{
		{ID: "summit", Label: "High terrain", Elevation: round10(hi), Modelled: true},
		{ID: "mid", Label: "Mid-mountain", Elevation: round10((pointElev + hi) / 2), Modelled: true},
		{ID: "base", Label: "Town", Elevation: pointElev},
	}
}

// Explicit builds bands from caller-supplied base and summit elevations.
func Explicit(base, summit float64) []compute.Band {
	return []compute.Band{
		{ID: "summit", Label: "Summit", Elevation: summit},
		{ID: "mid", Label: "Mid-mountain", Elevation: round10((base + summit) / 2), Modelled: true},
		{ID: "base", Label: "Base", Elevation: base},
	}
}

func round10(v float64) float64 { return math.Round(v/10) * 10 }

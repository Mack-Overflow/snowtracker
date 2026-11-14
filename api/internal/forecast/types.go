// Package forecast holds the provider-neutral ensemble data model.
package forecast

// Model is one ensemble forecast system (e.g. GEFS, ECMWF ENS).
// Precip and Temp are indexed [member][step]; missing values are NaN.
type Model struct {
	Name   string
	Precip [][]float32 // mm per hourly step
	Temp   [][]float32 // °C at Ensemble.RefElevation
	// Horizon is the number of leading steps for which every member has data.
	Horizon int
}

// Ensemble is the raw material for all computations at one coordinate.
type Ensemble struct {
	Provider         string
	Lat, Lon         float64
	RefElevation     float64 // metres; the elevation Temp was downscaled to
	Timezone         string
	UTCOffsetSeconds int
	// Times are hourly, in the location's local time, formatted 2006-01-02T15:04.
	Times  []string
	Models []Model
}

// Place is a geocoding result.
type Place struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Region      string  `json:"region,omitempty"`
	Country     string  `json:"country,omitempty"`
	CountryCode string  `json:"-"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Elevation   float64 `json:"elevation"`
	Kind        string  `json:"kind"` // GeoNames feature code, e.g. MT, PPL
	Timezone    string  `json:"timezone,omitempty"`
}

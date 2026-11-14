package provider

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// Field is one forecast on a regular lat/lon grid, for drawing maps. Unlike
// Ensemble it is a single series per point: either the average of an ensemble
// or one deterministic run.
type Field struct {
	Start time.Time // first step, UTC; steps are hourly
	Steps int
	// Per point, in the order the coordinates were given.
	Elevation []float64   // metres; the height Temp refers to
	Precip    [][]float32 // mm per hour; NaN where missing
	Temp      [][]float32 // °C at Elevation
}

// Field models.
const (
	FieldMean = "mean" // average of the 51 ECMWF ensemble runs
	FieldRun  = "run"  // Open-Meteo's best single deterministic model per place
)

type fieldPoint struct {
	Elevation float64 `json:"elevation"`
	Hourly    struct {
		Time   []string   `json:"time"`
		Precip []*float64 `json:"precipitation"`
		Temp   []*float64 `json:"temperature_2m"`
	} `json:"hourly"`
}

// Field fetches hourly precipitation and temperature for every coordinate in
// one request. Upstream bills one call per coordinate, so callers should keep
// grids small and cache the result.
func (o *OpenMeteo) Field(ctx context.Context, model string, lats, lons []float64) (*Field, error) {
	if len(lats) < 2 || len(lats) != len(lons) {
		return nil, fmt.Errorf("field needs at least two coordinates")
	}
	q := url.Values{}
	q.Set("latitude", joinFloats(lats))
	q.Set("longitude", joinFloats(lons))
	q.Set("hourly", "precipitation,temperature_2m")
	q.Set("forecast_days", strconv.Itoa(o.ForecastDays))
	q.Set("timezone", "GMT") // one shared time axis for the whole grid

	var base string
	switch model {
	case FieldMean:
		base = o.EnsembleURL
		q.Set("models", "ecmwf_ifs025_ensemble_mean")
	case FieldRun:
		base = o.ForecastURL + "/forecast"
	default:
		return nil, fmt.Errorf("unknown field model %q", model)
	}

	var raw []fieldPoint
	if err := o.getJSON(ctx, base+"?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	return parseField(raw, len(lats))
}

func parseField(raw []fieldPoint, want int) (*Field, error) {
	if len(raw) != want {
		return nil, fmt.Errorf("open-meteo: asked for %d points, got %d", want, len(raw))
	}
	times := raw[0].Hourly.Time
	if len(times) == 0 {
		return nil, fmt.Errorf("open-meteo: missing time axis")
	}
	start, err := time.Parse("2006-01-02T15:04", times[0])
	if err != nil {
		return nil, fmt.Errorf("open-meteo: bad time %q", times[0])
	}
	f := &Field{Start: start, Steps: len(times)}
	for _, p := range raw {
		if len(p.Hourly.Precip) != f.Steps || len(p.Hourly.Temp) != f.Steps {
			return nil, fmt.Errorf("open-meteo: ragged field")
		}
		f.Elevation = append(f.Elevation, p.Elevation)
		f.Precip = append(f.Precip, nullable(p.Hourly.Precip))
		f.Temp = append(f.Temp, nullable(p.Hourly.Temp))
	}
	return f, nil
}

// Package provider fetches raw forecast data from upstream sources.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/sync/errgroup"

	"precip-calendar/api/internal/forecast"
)

// Provider is the seam for adding further sources (national services,
// self-hosted GRIB decoding, ...). Covers lets a registry pick by location.
type Provider interface {
	Name() string
	Covers(lat, lon float64) bool
	Ensemble(ctx context.Context, lat, lon, elevation float64) (*forecast.Ensemble, error)
}

const userAgent = "pow-tracker/0.1"

// OpenMeteo talks to the keyless Open-Meteo APIs (CC BY 4.0, non-commercial tier).
type OpenMeteo struct {
	HTTP         *http.Client
	EnsembleURL  string
	ForecastURL  string
	GeocodingURL string
	ForecastDays int
}

func NewOpenMeteo() *OpenMeteo {
	return &OpenMeteo{
		HTTP:         &http.Client{Timeout: 25 * time.Second},
		EnsembleURL:  "https://ensemble-api.open-meteo.com/v1/ensemble",
		ForecastURL:  "https://api.open-meteo.com/v1",
		GeocodingURL: "https://geocoding-api.open-meteo.com/v1/search",
		ForecastDays: 10,
	}
}

func (o *OpenMeteo) Name() string                 { return "open-meteo" }
func (o *OpenMeteo) Covers(lat, lon float64) bool { return true }

// ModelsFor picks ensemble systems by location: two global systems everywhere,
// plus a regional one where it exists.
func ModelsFor(lat, lon float64) []string {
	models := []string{"gfs025", "ecmwf_ifs025"}
	switch {
	case lat >= 34 && lat <= 71 && lon >= -11 && lon <= 34:
		models = append(models, "icon_eu") // 13 km, ~5.5 days
	case lat >= 15 && lat <= 75 && lon >= -170 && lon <= -50:
		models = append(models, "gem_global")
	}
	return models
}

func (o *OpenMeteo) getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("open-meteo %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type ensembleResponse struct {
	Elevation        float64                    `json:"elevation"`
	Timezone         string                     `json:"timezone"`
	UTCOffsetSeconds int                        `json:"utc_offset_seconds"`
	Hourly           map[string]json.RawMessage `json:"hourly"`
}

func (o *OpenMeteo) Ensemble(ctx context.Context, lat, lon, elevation float64) (*forecast.Ensemble, error) {
	q := url.Values{}
	q.Set("latitude", ff(lat))
	q.Set("longitude", ff(lon))
	q.Set("elevation", ff(elevation))
	q.Set("hourly", "precipitation,temperature_2m")
	q.Set("models", strings.Join(ModelsFor(lat, lon), ","))
	q.Set("forecast_days", strconv.Itoa(o.ForecastDays))
	q.Set("timezone", "auto")

	var raw ensembleResponse
	if err := o.getJSON(ctx, o.EnsembleURL+"?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	ens, err := parseEnsemble(&raw)
	if err != nil {
		return nil, err
	}
	ens.Provider = o.Name()
	ens.Lat, ens.Lon, ens.RefElevation = lat, lon, elevation
	return ens, nil
}

// Keys look like precipitation_member07_ecmwf_ifs025_ensemble; the control run
// has no _memberNN part.
var seriesKey = regexp.MustCompile(`^(precipitation|temperature_2m)(?:_member(\d+))?_(.+)$`)

func parseEnsemble(raw *ensembleResponse) (*forecast.Ensemble, error) {
	ens := &forecast.Ensemble{Timezone: raw.Timezone, UTCOffsetSeconds: raw.UTCOffsetSeconds}
	if err := json.Unmarshal(raw.Hourly["time"], &ens.Times); err != nil || len(ens.Times) == 0 {
		return nil, fmt.Errorf("open-meteo: missing time axis")
	}
	n := len(ens.Times)

	type pair struct{ precip, temp []float32 }
	byModel := map[string]map[int]*pair{}
	for key, msg := range raw.Hourly {
		m := seriesKey.FindStringSubmatch(key)
		if m == nil {
			continue
		}
		member := 0
		if m[2] != "" {
			member, _ = strconv.Atoi(m[2])
		}
		var vals []*float64
		if err := json.Unmarshal(msg, &vals); err != nil || len(vals) != n {
			continue
		}
		series := nullable(vals)
		if byModel[m[3]] == nil {
			byModel[m[3]] = map[int]*pair{}
		}
		p := byModel[m[3]][member]
		if p == nil {
			p = &pair{}
			byModel[m[3]][member] = p
		}
		if m[1] == "precipitation" {
			p.precip = series
		} else {
			p.temp = series
		}
	}

	names := make([]string, 0, len(byModel))
	for name := range byModel {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		ids := make([]int, 0, len(byModel[name]))
		for id := range byModel[name] {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		model := forecast.Model{Name: name, Horizon: n}
		for _, id := range ids {
			p := byModel[name][id]
			if p.precip == nil || p.temp == nil {
				continue
			}
			h := validPrefix(p.precip, p.temp)
			if h == 0 {
				continue // member entirely missing
			}
			model.Horizon = min(model.Horizon, h)
			model.Precip = append(model.Precip, p.precip)
			model.Temp = append(model.Temp, p.temp)
		}
		if len(model.Precip) > 0 {
			ens.Models = append(ens.Models, model)
		}
	}
	if len(ens.Models) == 0 {
		return nil, fmt.Errorf("open-meteo: no usable ensemble members")
	}
	return ens, nil
}

// nullable converts upstream's nullable numbers to float32, NaN for null.
func nullable(vals []*float64) []float32 {
	out := make([]float32, len(vals))
	for i, v := range vals {
		if v == nil {
			out[i] = float32(math.NaN())
		} else {
			out[i] = float32(*v)
		}
	}
	return out
}

func validPrefix(a, b []float32) int {
	for i := range a {
		if a[i] != a[i] || b[i] != b[i] { // NaN
			return i
		}
	}
	return len(a)
}

type geocodeResponse struct {
	Results []struct {
		ID          int64   `json:"id"`
		Name        string  `json:"name"`
		Latitude    float64 `json:"latitude"`
		Longitude   float64 `json:"longitude"`
		Elevation   float64 `json:"elevation"`
		FeatureCode string  `json:"feature_code"`
		Country     string  `json:"country"`
		CountryCode string  `json:"country_code"`
		Admin1      string  `json:"admin1"`
		Timezone    string  `json:"timezone"`
	} `json:"results"`
}

const (
	geocodeFetch = 100  // upstream ranks by population, so mountain places sit deep
	searchTop    = 5    // best matches in upstream order
	searchHigh   = 5    // extra slots reserved for high places
	highPlaceM   = 1000 // metres
)

// geocode returns upstream matches for an exact place name, most populous first.
func (o *OpenMeteo) geocode(ctx context.Context, name string) ([]forecast.Place, error) {
	q := url.Values{}
	q.Set("name", name)
	q.Set("count", strconv.Itoa(geocodeFetch))
	q.Set("language", "en")
	var raw geocodeResponse
	if err := o.getJSON(ctx, o.GeocodingURL+"?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	places := make([]forecast.Place, 0, len(raw.Results))
	for _, r := range raw.Results {
		if !usefulKind(r.FeatureCode) {
			continue
		}
		places = append(places, forecast.Place{
			ID: r.ID, Name: r.Name, Region: r.Admin1, Country: r.Country, CountryCode: r.CountryCode,
			Lat: r.Latitude, Lon: r.Longitude, Elevation: r.Elevation,
			Kind: r.FeatureCode, Timezone: r.Timezone,
		})
	}
	return places, nil
}

var peakKinds = map[string]bool{
	"MT": true, "MTS": true, "PK": true, "PKS": true, "VLC": true,
	"RDGE": true, "PASS": true, "GLCR": true, "HLL": true, "HLLS": true,
}

// usefulKind keeps settlements, terrain and resorts, and drops the heliports,
// schools and buildings that share their names.
func usefulKind(code string) bool {
	switch {
	case strings.HasPrefix(code, "PPL"), strings.HasPrefix(code, "ADM"), peakKinds[code]:
		return true
	}
	switch code {
	case "RSRT", "PRK", "ISL", "VAL", "LK", "AREA", "RGN":
		return true
	}
	return false
}

// shortlist keeps the most populous matches and then adds high places, so a
// mountain village isn't hidden behind every larger town of the same name.
func shortlist(places []forecast.Place) []forecast.Place {
	out := append([]forecast.Place(nil), places[:min(searchTop, len(places))]...)
	for _, p := range places[len(out):] {
		if len(out) == searchTop+searchHigh {
			break
		}
		if p.Elevation >= highPlaceM || peakKinds[p.Kind] {
			out = append(out, p)
		}
	}
	return out
}

var regionAbbrev = map[string]string{
	"al": "alabama", "ak": "alaska", "az": "arizona", "ar": "arkansas", "ca": "california",
	"co": "colorado", "ct": "connecticut", "de": "delaware", "fl": "florida", "ga": "georgia",
	"hi": "hawaii", "id": "idaho", "il": "illinois", "in": "indiana", "ia": "iowa",
	"ks": "kansas", "ky": "kentucky", "la": "louisiana", "me": "maine", "md": "maryland",
	"ma": "massachusetts", "mi": "michigan", "mn": "minnesota", "ms": "mississippi",
	"mo": "missouri", "mt": "montana", "ne": "nebraska", "nv": "nevada", "nh": "new hampshire",
	"nj": "new jersey", "nm": "new mexico", "ny": "new york", "nc": "north carolina",
	"nd": "north dakota", "oh": "ohio", "ok": "oklahoma", "or": "oregon", "pa": "pennsylvania",
	"ri": "rhode island", "sc": "south carolina", "sd": "south dakota", "tn": "tennessee",
	"tx": "texas", "ut": "utah", "vt": "vermont", "va": "virginia", "wa": "washington",
	"wv": "west virginia", "wi": "wisconsin", "wy": "wyoming",
	"ab": "alberta", "bc": "british columbia", "mb": "manitoba", "nb": "new brunswick",
	"nl": "newfoundland and labrador", "ns": "nova scotia", "on": "ontario", "qc": "quebec",
	"sk": "saskatchewan", "yt": "yukon",
	"usa": "united states", "uk": "united kingdom",
}

// inRegion reports whether a place lies in the state, province or country the
// user typed. Two-letter codes are tried as a state first, then as a country.
func inRegion(p forecast.Place, region string) bool {
	region = strings.ToLower(region)
	if strings.EqualFold(p.Region, region) || strings.EqualFold(p.Country, region) || strings.EqualFold(p.CountryCode, region) {
		return true
	}
	full, ok := regionAbbrev[region]
	return ok && (strings.EqualFold(p.Region, full) || strings.EqualFold(p.Country, full))
}

var genericWords = map[string]bool{"resort": true, "ski": true, "area": true, "village": true}

// Search finds towns, peaks and other named places.
//
// The upstream geocoder matches on the place name alone, so "Brighton Utah"
// finds nothing there. Trailing words are therefore also tried as a region
// ("Brighton" within "Utah"), and those matches are listed first.
func (o *OpenMeteo) Search(ctx context.Context, query string) ([]forecast.Place, error) {
	tokens := strings.FieldsFunc(query, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	if len(tokens) == 0 {
		return nil, nil
	}

	// Slot 0 is the whole query; slots 1 and 2 treat the last one or two
	// words as a region.
	const maxRegionWords = 2
	lists := make([][]forecast.Place, 1+maxRegionWords)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		ps, err := o.geocode(gctx, strings.Join(tokens, " "))
		lists[0] = shortlist(ps)
		return err
	})
	for k := 1; k <= maxRegionWords && k < len(tokens); k++ {
		g.Go(func() error {
			name := strings.Join(tokens[:len(tokens)-k], " ")
			region := strings.Join(tokens[len(tokens)-k:], " ")
			ps, err := o.geocode(gctx, name)
			for _, p := range ps {
				if inRegion(p, region) && len(lists[k]) < searchTop+searchHigh {
					lists[k] = append(lists[k], p)
				}
			}
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	out := []forecast.Place{}
	seen := map[int64]bool{}
	for _, list := range [][]forecast.Place{lists[2], lists[1], lists[0]} {
		for _, p := range list {
			if !seen[p.ID] {
				seen[p.ID] = true
				out = append(out, p)
			}
		}
	}
	if len(out) > 0 {
		return out, nil
	}

	// "Solitude Mountain Resort" is known upstream only as "Solitude".
	var core []string
	for _, t := range tokens {
		if !genericWords[strings.ToLower(t)] {
			core = append(core, t)
		}
	}
	// Then shorten from the right: "Solitude Mountain" → "Solitude".
	for n := len(core); n > 0 && n > len(core)-3; n-- {
		if n == len(tokens) {
			continue // the whole query, already tried
		}
		ps, err := o.geocode(ctx, strings.Join(core[:n], " "))
		if err != nil {
			return nil, err
		}
		if len(ps) > 0 {
			return shortlist(ps), nil
		}
	}
	return out, nil
}

// Elevations returns terrain height (90 m DEM) for each coordinate. The
// dedicated endpoint is tried first; the ensemble endpoint, which also reports
// DEM height per coordinate, is the fallback.
func (o *OpenMeteo) Elevations(ctx context.Context, lats, lons []float64) ([]float64, error) {
	q := url.Values{}
	q.Set("latitude", joinFloats(lats))
	q.Set("longitude", joinFloats(lons))

	fast, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	var direct struct {
		Elevation []float64 `json:"elevation"`
	}
	if err := o.getJSON(fast, o.ForecastURL+"/elevation?"+q.Encode(), &direct); err == nil && len(direct.Elevation) == len(lats) {
		return direct.Elevation, nil
	}

	q.Set("hourly", "temperature_2m")
	q.Set("models", "gfs025")
	q.Set("forecast_days", "1")
	var viaEnsemble []struct {
		Elevation float64 `json:"elevation"`
	}
	if err := o.getJSON(ctx, o.EnsembleURL+"?"+q.Encode(), &viaEnsemble); err != nil {
		return nil, err
	}
	if len(viaEnsemble) != len(lats) {
		return nil, fmt.Errorf("open-meteo: elevation count mismatch")
	}
	out := make([]float64, len(lats))
	for i, v := range viaEnsemble {
		out[i] = v.Elevation
	}
	return out, nil
}

func ff(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }

func joinFloats(vs []float64) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = ff(v)
	}
	return strings.Join(parts, ",")
}

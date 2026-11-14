// Package compute turns raw ensemble members into the numbers the UI shows:
// hourly and daily precipitation probabilities, storm windows, and a rain/snow
// split for each elevation band. It is pure Go with no I/O.
package compute

import (
	"math"
	"sort"
	"time"

	"precip-calendar/api/internal/forecast"
)

// Tunables. All precipitation is liquid-equivalent millimetres.
const (
	LapseRate = 0.0065 // °C per metre, matches the upstream downscaling

	wetRoll3Mm      = 0.5  // a member is "wet" at an hour if its 3 h sum reaches this
	stormEnterProb  = 0.35 // share of wet members that opens a storm window
	stormMergeGapH  = 6    // windows separated by at most this many hours merge
	stormMinHours   = 3
	stormMemberMm   = 2.0  // a member "has the storm" if it totals at least this
	stormMinLikely  = 0.25 // windows less likely than this are dropped
	dayWetMm        = 1.0
	dayHeavyMm      = 10.0
	daySnowCm       = 1.0
	minDayHours     = 18 // trailing partial days shorter than this are dropped
	snowAllBelowC   = 0.0
	rainAllAboveC   = 2.0
	maxForecastStep = 240
)

// Band is one elevation at which rain/snow is evaluated.
type Band struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	Elevation float64 `json:"elevation"`
	// Modelled is true when the elevation was derived from terrain rather than
	// being the named place itself.
	Modelled bool `json:"modelled"`
}

type Quantiles struct {
	P10 float64 `json:"p10"`
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
}

type ModelInfo struct {
	Name         string `json:"name"`
	Members      int    `json:"members"`
	HorizonHours int    `json:"horizonHours"`
}

type HourlyBand struct {
	TempC     []float64 `json:"tempC"`
	SnowShare []float64 `json:"snowShare"` // 0 = all rain, 1 = all snow
}

type Hourly struct {
	Times   []string     `json:"times"`
	WetProb []float64    `json:"wetProb"`
	MeanMm  []float64    `json:"meanMm"`
	P90Mm   []float64    `json:"p90Mm"`
	Bands   []HourlyBand `json:"bands"`
}

type DayBand struct {
	SnowCm   Quantiles `json:"snowCm"`
	SnowProb float64   `json:"snowProb"`
	RainMm   float64   `json:"rainMm"` // ensemble mean
	Kind     string    `json:"kind"`   // snow | mixed | rain
	TempMinC float64   `json:"tempMinC"`
	TempMaxC float64   `json:"tempMaxC"`
}

type Day struct {
	Date       string    `json:"date"`
	Hours      int       `json:"hours"`
	Members    int       `json:"members"`
	PrecipProb float64   `json:"precipProb"`
	HeavyProb  float64   `json:"heavyProb"`
	PrecipMm   Quantiles `json:"precipMm"`
	Bands      []DayBand `json:"bands"`
}

type StormBand struct {
	SnowCm Quantiles `json:"snowCm"`
	RainMm float64   `json:"rainMm"`
	Kind   string    `json:"kind"`
}

type Storm struct {
	ID         string             `json:"id"`
	Start      string             `json:"start"`
	End        string             `json:"end"` // exclusive
	Peak       string             `json:"peak"`
	Hours      int                `json:"hours"`
	Likelihood float64            `json:"likelihood"`
	ByModel    map[string]float64 `json:"byModel"`
	Members    int                `json:"members"`
	PrecipMm   Quantiles          `json:"precipMm"`
	Intensity  string             `json:"intensity"` // light | moderate | heavy
	SnowLevelM *float64           `json:"snowLevelM"`
	Underway   bool               `json:"underway"`  // already wet at the first hour
	OpenEnded  bool               `json:"openEnded"` // still wet at the forecast horizon
	Bands      []StormBand        `json:"bands"`
}

type Result struct {
	Models []ModelInfo `json:"models"`
	Hourly Hourly      `json:"hourly"`
	Days   []Day       `json:"days"`
	Storms []Storm     `json:"storms"`
}

type member struct {
	model   int
	precip  []float32
	temp    []float32
	roll3   []float32
	horizon int
}

// Build computes everything for the given bands. Hours before `now` (in the
// location's own clock) are dropped so the first day is "rest of today".
func Build(ens *forecast.Ensemble, bands []Band, now time.Time) *Result {
	start := firstStep(ens, now)
	res := &Result{}
	var members []member
	steps := 0
	for mi, m := range ens.Models {
		h := min(m.Horizon, maxForecastStep) - start
		if h <= 0 {
			continue
		}
		res.Models = append(res.Models, ModelInfo{Name: m.Name, Members: len(m.Precip), HorizonHours: h})
		steps = max(steps, h)
		for k := range m.Precip {
			mem := member{model: mi, precip: m.Precip[k][start : start+h], temp: m.Temp[k][start : start+h], horizon: h}
			mem.roll3 = rolling3(mem.precip)
			members = append(members, mem)
		}
	}
	if steps == 0 {
		return res
	}
	times := ens.Times[start : start+steps]
	offsets := make([]float32, len(bands))
	for b, band := range bands {
		offsets[b] = float32((ens.RefElevation - band.Elevation) * LapseRate)
	}

	res.Hourly = buildHourly(times, members, offsets)
	res.Days = buildDays(times, members, offsets, res.Hourly)
	res.Storms = buildStorms(ens, times, members, offsets, res.Hourly)
	return res
}

func firstStep(ens *forecast.Ensemble, now time.Time) int {
	local := now.UTC().Add(time.Duration(ens.UTCOffsetSeconds) * time.Second).Format("2006-01-02T15:00")
	i := sort.SearchStrings(ens.Times, local)
	if i >= len(ens.Times) {
		return 0
	}
	return i
}

func rolling3(p []float32) []float32 {
	out := make([]float32, len(p))
	for t := range p {
		s := p[t]
		if t > 0 {
			s += p[t-1]
		}
		if t+1 < len(p) {
			s += p[t+1]
		}
		out[t] = s
	}
	return out
}

// SnowFraction is the share of precipitation falling as snow at temperature t.
func SnowFraction(t float32) float32 {
	switch {
	case t <= snowAllBelowC:
		return 1
	case t >= rainAllAboveC:
		return 0
	}
	return (rainAllAboveC - t) / (rainAllAboveC - snowAllBelowC)
}

// SnowCmPerMm is the depth of new snow per mm of liquid: 10:1 near freezing,
// drier and fluffier (up to 15:1) as it gets colder.
func SnowCmPerMm(t float32) float32 {
	switch {
	case t >= -2:
		return 1.0
	case t <= -12:
		return 1.5
	}
	return 1.0 + (-2-t)/10*0.5
}

func buildHourly(times []string, members []member, offsets []float32) Hourly {
	n := len(times)
	h := Hourly{
		Times:   times,
		WetProb: make([]float64, n),
		MeanMm:  make([]float64, n),
		P90Mm:   make([]float64, n),
		Bands:   make([]HourlyBand, len(offsets)),
	}
	for b := range offsets {
		h.Bands[b] = HourlyBand{TempC: make([]float64, n), SnowShare: make([]float64, n)}
	}
	scratch := make([]float64, 0, len(members))
	snow := make([]float64, len(offsets))
	for t := 0; t < n; t++ {
		scratch = scratch[:0]
		wet, tempSum := 0, 0.0
		for b := range snow {
			snow[b] = 0
		}
		for i := range members {
			m := &members[i]
			if m.horizon <= t {
				continue
			}
			scratch = append(scratch, float64(m.precip[t]))
			if m.roll3[t] >= wetRoll3Mm {
				wet++
			}
			tempSum += float64(m.temp[t])
			for b, off := range offsets {
				snow[b] += float64(SnowFraction(m.temp[t] + off))
			}
		}
		active := float64(len(scratch))
		if active == 0 {
			continue
		}
		h.WetProb[t] = round(float64(wet)/active, 2)
		h.MeanMm[t] = round(mean(scratch), 2)
		sort.Float64s(scratch)
		h.P90Mm[t] = round(quantileSorted(scratch, 0.9), 2)
		for b, off := range offsets {
			h.Bands[b].TempC[t] = round(tempSum/active+float64(off), 1)
			h.Bands[b].SnowShare[t] = round(snow[b]/active, 2)
		}
	}
	return h
}

// windowTotals sums precipitation, snow depth and rain over [s, e) for every
// member whose data covers the whole window.
type windowTotals struct {
	models  []int
	precip  []float64
	snowCm  [][]float64 // [band][member]
	rainMm  [][]float64
	snowWet []float64 // per band: total snow water across members
	// precip-weighted sum of the elevation where snow and rain are 50/50
	levelSum, levelWeight float64
}

func totalsFor(members []member, offsets []float32, s, e int, refElev float64) windowTotals {
	w := windowTotals{
		snowCm:  make([][]float64, len(offsets)),
		rainMm:  make([][]float64, len(offsets)),
		snowWet: make([]float64, len(offsets)),
	}
	mid := float32((snowAllBelowC + rainAllAboveC) / 2)
	for i := range members {
		m := &members[i]
		if m.horizon < e {
			continue
		}
		total := 0.0
		for t := s; t < e; t++ {
			p := float64(m.precip[t])
			total += p
			w.levelSum += p * (refElev + float64(m.temp[t]-mid)/LapseRate)
			w.levelWeight += p
		}
		w.models = append(w.models, m.model)
		w.precip = append(w.precip, total)
		for b, off := range offsets {
			snowCm, rain := 0.0, 0.0
			for t := s; t < e; t++ {
				p := m.precip[t]
				if p == 0 {
					continue
				}
				temp := m.temp[t] + off
				sf := SnowFraction(temp)
				snowCm += float64(p * sf * SnowCmPerMm(temp))
				rain += float64(p * (1 - sf))
				w.snowWet[b] += float64(p * sf)
			}
			w.snowCm[b] = append(w.snowCm[b], snowCm)
			w.rainMm[b] = append(w.rainMm[b], rain)
		}
	}
	return w
}

func kindOf(snowWater, precip float64) string {
	if precip <= 0 {
		return "rain"
	}
	switch share := snowWater / precip; {
	case share >= 0.8:
		return "snow"
	case share <= 0.2:
		return "rain"
	}
	return "mixed"
}

func buildDays(times []string, members []member, offsets []float32, hourly Hourly) []Day {
	var days []Day
	n := len(times)
	for s := 0; s < n; {
		e := s
		for e < n && times[e][:10] == times[s][:10] {
			e++
		}
		// The first day may be short (rest of today); a short last day is noise.
		if s > 0 && e-s < minDayHours {
			break
		}
		w := totalsFor(members, offsets, s, e, 0)
		if len(w.precip) == 0 {
			break
		}
		precipSum := sum(w.precip)
		day := Day{
			Date:       times[s][:10],
			Hours:      e - s,
			Members:    len(w.precip),
			PrecipProb: round(exceed(w.precip, dayWetMm), 2),
			HeavyProb:  round(exceed(w.precip, dayHeavyMm), 2),
			PrecipMm:   quantiles(w.precip, 1),
			Bands:      make([]DayBand, len(offsets)),
		}
		for b := range offsets {
			tmin, tmax := math.Inf(1), math.Inf(-1)
			for t := s; t < e; t++ {
				tmin = math.Min(tmin, hourly.Bands[b].TempC[t])
				tmax = math.Max(tmax, hourly.Bands[b].TempC[t])
			}
			day.Bands[b] = DayBand{
				SnowCm:   quantiles(w.snowCm[b], 1),
				SnowProb: round(exceed(w.snowCm[b], daySnowCm), 2),
				RainMm:   round(mean(w.rainMm[b]), 1),
				Kind:     kindOf(w.snowWet[b], precipSum),
				TempMinC: tmin,
				TempMaxC: tmax,
			}
		}
		days = append(days, day)
		s = e
	}
	return days
}

func buildStorms(ens *forecast.Ensemble, times []string, members []member, offsets []float32, hourly Hourly) []Storm {
	n := len(times)
	type seg struct{ s, e int }
	var segs []seg
	for t := 0; t < n; {
		if hourly.WetProb[t] < stormEnterProb {
			t++
			continue
		}
		s := t
		for t < n && hourly.WetProb[t] >= stormEnterProb {
			t++
		}
		if len(segs) > 0 && s-segs[len(segs)-1].e <= stormMergeGapH {
			segs[len(segs)-1].e = t
		} else {
			segs = append(segs, seg{s, t})
		}
	}

	storms := []Storm{}
	for _, sg := range segs {
		if sg.e-sg.s < stormMinHours {
			continue
		}
		w := totalsFor(members, offsets, sg.s, sg.e, ens.RefElevation)
		if len(w.precip) == 0 {
			continue
		}
		likelihood := exceed(w.precip, stormMemberMm)
		if likelihood < stormMinLikely {
			continue
		}
		peak := sg.s
		for t := sg.s; t < sg.e; t++ {
			if hourly.MeanMm[t] > hourly.MeanMm[peak] {
				peak = t
			}
		}
		end := times[n-1]
		if sg.e < n {
			end = times[sg.e]
		}
		st := Storm{
			ID:         times[sg.s],
			Start:      times[sg.s],
			End:        end,
			Peak:       times[peak],
			Hours:      sg.e - sg.s,
			Likelihood: round(likelihood, 2),
			ByModel:    map[string]float64{},
			Members:    len(w.precip),
			PrecipMm:   quantiles(w.precip, 1),
			Underway:   sg.s == 0,
			OpenEnded:  sg.e == n,
			Bands:      make([]StormBand, len(offsets)),
		}
		st.Intensity = intensity(st.PrecipMm.P50, st.Hours)
		hits, counts := map[int]int{}, map[int]int{}
		for i, mi := range w.models {
			counts[mi]++
			if w.precip[i] >= stormMemberMm {
				hits[mi]++
			}
		}
		for mi, c := range counts {
			st.ByModel[ens.Models[mi].Name] = round(float64(hits[mi])/float64(c), 2)
		}
		if w.levelWeight > 0 {
			level := math.Round(w.levelSum/w.levelWeight/50) * 50
			st.SnowLevelM = &level
		}
		precipSum := sum(w.precip)
		for b := range offsets {
			st.Bands[b] = StormBand{
				SnowCm: quantiles(w.snowCm[b], 1),
				RainMm: round(mean(w.rainMm[b]), 1),
				Kind:   kindOf(w.snowWet[b], precipSum),
			}
		}
		storms = append(storms, st)
	}
	return storms
}

// intensity classifies by the median storm total, scaled to a 24 h rate so a
// long drizzle and a short burst aren't labelled alike.
func intensity(p50Mm float64, hours int) string {
	perDay := p50Mm
	if hours > 24 {
		perDay = p50Mm * 24 / float64(hours)
	}
	switch {
	case perDay >= 25:
		return "heavy"
	case perDay >= 8:
		return "moderate"
	}
	return "light"
}

func sum(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	return sum(v) / float64(len(v))
}

// exceed is the share of values at or above threshold.
func exceed(v []float64, threshold float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := 0
	for _, x := range v {
		if x >= threshold {
			c++
		}
	}
	return float64(c) / float64(len(v))
}

func quantiles(v []float64, decimals int) Quantiles {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return Quantiles{
		P10: round(quantileSorted(s, 0.1), decimals),
		P50: round(quantileSorted(s, 0.5), decimals),
		P90: round(quantileSorted(s, 0.9), decimals),
	}
}

func quantileSorted(s []float64, q float64) float64 {
	if len(s) == 0 {
		return 0
	}
	pos := q * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := min(lo+1, len(s)-1)
	return s[lo] + (s[hi]-s[lo])*(pos-float64(lo))
}

func round(v float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(v*p) / p
}

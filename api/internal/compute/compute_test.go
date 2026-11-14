package compute

import (
	"math"
	"testing"
	"time"

	"precip-calendar/api/internal/forecast"
)

// synthetic builds an ensemble starting 2026-01-10T00:00 local (UTC-8).
// wetMembers of `members` get 1 mm/h during [stormStart, stormEnd).
func synthetic(steps, members, wetMembers, stormStart, stormEnd int, tempC float32) *forecast.Ensemble {
	base := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	ens := &forecast.Ensemble{RefElevation: 1000, UTCOffsetSeconds: -8 * 3600}
	for i := 0; i < steps; i++ {
		ens.Times = append(ens.Times, base.Add(time.Duration(i)*time.Hour).Format("2006-01-02T15:04"))
	}
	m := forecast.Model{Name: "test", Horizon: steps}
	for k := 0; k < members; k++ {
		p, t := make([]float32, steps), make([]float32, steps)
		for i := range t {
			t[i] = tempC
			if k < wetMembers && i >= stormStart && i < stormEnd {
				p[i] = 1
			}
		}
		m.Precip, m.Temp = append(m.Precip, p), append(m.Temp, t)
	}
	ens.Models = []forecast.Model{m}
	return ens
}

var (
	testBands = []Band{{ID: "summit", Elevation: 2000}, {ID: "base", Elevation: 1000}}
	// 2026-01-10T00:30 local at UTC-8, so no hours are trimmed.
	testNow = time.Date(2026, 1, 10, 8, 30, 0, 0, time.UTC)
)

func TestStormWindow(t *testing.T) {
	ens := synthetic(72, 10, 8, 24, 36, 3)
	res := Build(ens, testBands, testNow)

	if len(res.Storms) != 1 {
		t.Fatalf("want 1 storm, got %d: %+v", len(res.Storms), res.Storms)
	}
	s := res.Storms[0]
	// The 3 h rolling window opens the storm one hour early and closes it one late.
	if s.Start != "2026-01-11T00:00" && s.Start != "2026-01-10T23:00" {
		t.Errorf("start = %s", s.Start)
	}
	if s.Likelihood != 0.8 {
		t.Errorf("likelihood = %v, want 0.8", s.Likelihood)
	}
	if s.PrecipMm.P50 != 12 {
		t.Errorf("p50 precip = %v, want 12", s.PrecipMm.P50)
	}
	if s.Underway || s.OpenEnded {
		t.Errorf("storm should be fully inside the horizon: %+v", s)
	}
	// 3 °C at 1000 m is all rain; 1000 m higher is -3.5 °C, all snow.
	if got := s.Bands[1]; got.Kind != "rain" || got.SnowCm.P90 != 0 || got.RainMm != 9.6 {
		t.Errorf("base band = %+v", got)
	}
	if got := s.Bands[0]; got.Kind != "snow" || got.SnowCm.P50 < 12 || got.RainMm != 0 {
		t.Errorf("summit band = %+v", got)
	}
	// Rain/snow line sits where it is 1 °C: (3-1)/0.0065 = 308 m above 1000 m.
	if s.SnowLevelM == nil || *s.SnowLevelM != 1300 {
		t.Errorf("snow level = %v, want 1300", s.SnowLevelM)
	}
}

func TestDaysBucketInLocalTime(t *testing.T) {
	ens := synthetic(72, 10, 8, 24, 36, 3)
	res := Build(ens, testBands, testNow)
	if len(res.Days) != 3 {
		t.Fatalf("want 3 days, got %d", len(res.Days))
	}
	if d := res.Days[0]; d.Date != "2026-01-10" || d.PrecipProb != 0 {
		t.Errorf("day 0 = %+v", d)
	}
	d := res.Days[1]
	if d.Date != "2026-01-11" || d.PrecipProb != 0.8 || d.HeavyProb != 0.8 || d.PrecipMm.P50 != 12 {
		t.Errorf("day 1 = %+v", d)
	}
	if d.Bands[0].SnowProb != 0.8 || d.Bands[1].SnowProb != 0 {
		t.Errorf("day 1 snow probs = %v / %v", d.Bands[0].SnowProb, d.Bands[1].SnowProb)
	}
}

func TestPastHoursAreTrimmed(t *testing.T) {
	ens := synthetic(72, 10, 8, 24, 36, 3)
	// 2026-01-10T20:10 local.
	now := time.Date(2026, 1, 11, 4, 10, 0, 0, time.UTC)
	res := Build(ens, testBands, now)
	if got := res.Hourly.Times[0]; got != "2026-01-10T20:00" {
		t.Errorf("first hour = %s", got)
	}
	if d := res.Days[0]; d.Hours != 4 {
		t.Errorf("first day hours = %d, want 4", d.Hours)
	}
}

func TestShortHorizonModelIsExcludedBeyondItsData(t *testing.T) {
	ens := synthetic(72, 10, 10, 24, 36, 3)
	// A second model that stays dry but only reaches hour 30: it must not
	// dilute a storm that ends after its data runs out.
	dry := forecast.Model{Name: "short", Horizon: 30}
	for k := 0; k < 10; k++ {
		p, tt := make([]float32, 72), make([]float32, 72)
		for i := 30; i < 72; i++ {
			p[i], tt[i] = float32(math.NaN()), float32(math.NaN())
		}
		dry.Precip, dry.Temp = append(dry.Precip, p), append(dry.Temp, tt)
	}
	ens.Models = append(ens.Models, dry)
	res := Build(ens, testBands, testNow)
	if len(res.Storms) != 1 {
		t.Fatalf("want 1 storm, got %d", len(res.Storms))
	}
	if s := res.Storms[0]; s.Members != 10 || s.Likelihood != 1 {
		t.Errorf("members = %d likelihood = %v, want 10 / 1", s.Members, s.Likelihood)
	}
	if d := res.Days[0]; d.Members != 20 {
		t.Errorf("day 0 should pool both models, got %d members", d.Members)
	}
}

func TestSnowModel(t *testing.T) {
	for _, c := range []struct {
		temp float32
		want float32
	}{{-5, 1}, {0, 1}, {1, 0.5}, {2, 0}, {8, 0}} {
		if got := SnowFraction(c.temp); got != c.want {
			t.Errorf("SnowFraction(%v) = %v, want %v", c.temp, got, c.want)
		}
	}
	if SnowCmPerMm(0) != 1 || SnowCmPerMm(-20) != 1.5 || SnowCmPerMm(-7) != 1.25 {
		t.Errorf("snow ratio wrong: %v %v %v", SnowCmPerMm(0), SnowCmPerMm(-20), SnowCmPerMm(-7))
	}
}

func TestQuantile(t *testing.T) {
	q := quantiles([]float64{5, 1, 3, 2, 4}, 2)
	if q.P50 != 3 || q.P10 != 1.4 || q.P90 != 4.6 {
		t.Errorf("quantiles = %+v", q)
	}
}

func BenchmarkBuild(b *testing.B) {
	ens := synthetic(240, 103, 60, 40, 90, 1)
	bands := []Band{{Elevation: 3000}, {Elevation: 2000}, {Elevation: 1000}}
	b.ReportAllocs()
	for b.Loop() {
		Build(ens, bands, testNow)
	}
}

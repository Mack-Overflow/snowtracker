package httpapi

import (
	"context"
	"encoding/binary"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"precip-calendar/api/internal/forecast"
	"precip-calendar/api/internal/provider"
	"precip-calendar/api/internal/store"
)

type fakeSource struct {
	provider.Provider
	calls      int
	lats, lons []float64
}

func (f *fakeSource) Search(context.Context, string) ([]forecast.Place, error) { return nil, nil }
func (f *fakeSource) Elevations(context.Context, []float64, []float64) ([]float64, error) {
	return nil, nil
}
func (f *fakeSource) Field(_ context.Context, _ string, lats, lons []float64) (*provider.Field, error) {
	f.calls++
	f.lats, f.lons = lats, lons
	out := &provider.Field{Start: time.Unix(3600, 0).UTC(), Steps: 2}
	for i := range lats {
		out.Elevation = append(out.Elevation, float64(100*i))
		out.Precip = append(out.Precip, []float32{0.25, float32(math.NaN())})
		out.Temp = append(out.Temp, []float32{-1.5, float32(i)})
	}
	return out, nil
}

func TestFieldEndpoint(t *testing.T) {
	src := &fakeSource{}
	s := &Server{Source: src, Hot: store.NewHot(time.Hour), Fields: store.NewHot(time.Hour), Now: time.Now}
	r := s.Router()
	get := func(url string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		return w
	}

	for _, bad := range []string{
		"/api/field?step=0.25&i0=1&j0=0&nx=3&ny=3",   // odd origin
		"/api/field?step=0.25&i0=0&j0=0&nx=4&ny=3",   // even size
		"/api/field?step=0.3&i0=0&j0=0&nx=3&ny=3",    // unknown step
		"/api/field?step=0.25&i0=0&j0=0&nx=13&ny=13", // too many points
		"/api/field?step=4&i0=0&j0=20&nx=3&ny=3",     // past 85°N
	} {
		if w := get(bad); w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", bad, w.Code)
		}
	}

	const url = "/api/field?step=0.5&i0=-360&j0=90&nx=3&ny=5&model=run"
	w := get(url)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body)
	}
	if src.lats[0] != 45 || src.lons[0] != -180 || src.lats[14] != 47 || src.lons[14] != -179 {
		t.Errorf("coords: first %v,%v last %v,%v", src.lats[0], src.lons[0], src.lats[14], src.lons[14])
	}
	b := w.Body.Bytes()
	const n, steps = 15, 2
	if want := 32 + 4*n + 2*2*n*steps; len(b) != want {
		t.Fatalf("body is %d bytes, want %d", len(b), want)
	}
	le := binary.LittleEndian
	if string(b[:4]) != "PCF1" || le.Uint16(b[4:]) != 3 || le.Uint16(b[6:]) != 5 || le.Uint16(b[8:]) != steps {
		t.Errorf("bad header % x", b[:12])
	}
	if south := math.Float32frombits(le.Uint32(b[12:])); south != 45 {
		t.Errorf("south = %v", south)
	}
	if start := math.Float64frombits(le.Uint64(b[24:])); start != 3600 {
		t.Errorf("start = %v", start)
	}
	if e := math.Float32frombits(le.Uint32(b[32+4:])); e != 100 {
		t.Errorf("elevation[1] = %v", e)
	}
	i16 := func(off int) int16 { return int16(le.Uint16(b[off:])) }
	precip, temp := 32+4*n, 32+4*n+2*n*steps
	if i16(precip) != 25 || i16(precip+2*n) != -32768 {
		t.Errorf("precip = %d, %d", i16(precip), i16(precip+2*n))
	}
	if i16(temp) != -150 || i16(temp+2*n+2*14) != 1400 {
		t.Errorf("temp = %d, %d", i16(temp), i16(temp+2*n+2*14))
	}

	if get(url); src.calls != 1 {
		t.Errorf("upstream called %d times, want 1 (second request should hit the cache)", src.calls)
	}
}

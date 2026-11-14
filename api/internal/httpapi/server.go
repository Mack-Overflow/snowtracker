// Package httpapi exposes search and forecast endpoints over Gin.
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"

	"precip-calendar/api/internal/compute"
	"precip-calendar/api/internal/forecast"
	"precip-calendar/api/internal/provider"
	"precip-calendar/api/internal/store"
	"precip-calendar/api/internal/terrain"
)

// Source is what the server needs from upstream. *provider.OpenMeteo satisfies it.
type Source interface {
	provider.Provider
	Search(ctx context.Context, query string) ([]forecast.Place, error)
	Elevations(ctx context.Context, lats, lons []float64) ([]float64, error)
	Field(ctx context.Context, model string, lats, lons []float64) (*provider.Field, error)
}

type Server struct {
	Source  Source
	Hot     *store.Hot
	Fields  *store.Hot // map fields: cached, never archived
	Archive store.Archiver
	WebDir  string // optional: built frontend to serve
	Now     func() time.Time

	flights singleflight.Group
}

type location struct {
	Name             string  `json:"name"`
	Kind             string  `json:"kind,omitempty"`
	Lat              float64 `json:"lat"`
	Lon              float64 `json:"lon"`
	Elevation        float64 `json:"elevation"`
	Timezone         string  `json:"timezone"`
	UTCOffsetSeconds int     `json:"utcOffsetSeconds"`
}

type sourceInfo struct {
	Provider    string `json:"provider"`
	Attribution string `json:"attribution"`
}

type forecastResponse struct {
	Location    location       `json:"location"`
	GeneratedAt time.Time      `json:"generatedAt"`
	Source      sourceInfo     `json:"source"`
	Bands       []compute.Band `json:"bands"`
	Notes       []string       `json:"notes"`
	*compute.Result
}

func (s *Server) Router() *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	api := r.Group("/api")
	api.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	api.GET("/search", s.search)
	api.GET("/forecast", s.forecast)
	api.GET("/field", s.field)

	if s.WebDir != "" {
		r.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
				return
			}
			file := filepath.Join(s.WebDir, filepath.Clean("/"+c.Request.URL.Path))
			if st, err := os.Stat(file); err == nil && !st.IsDir() {
				c.File(file)
				return
			}
			c.File(filepath.Join(s.WebDir, "index.html"))
		})
	}
	return r
}

func (s *Server) search(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if len(q) < 2 {
		c.JSON(http.StatusOK, gin.H{"results": []forecast.Place{}})
		return
	}
	places, err := s.Source.Search(c.Request.Context(), q)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "search is unavailable: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": places})
}

type forecastParams struct {
	lat, lon     float64
	name, kind   string
	elevation    *float64
	base, summit *float64
}

// key identifies everything the cached body depends on. It doubles as an
// archive directory name, so it is built only from numbers and letters.
func (p forecastParams) key() string {
	k := fmt.Sprintf("%.3f_%.3f", p.lat, p.lon)
	if p.elevation != nil {
		k += fmt.Sprintf("_e%.0f", *p.elevation)
	}
	if p.kind != "" {
		k += "_k" + p.kind
	}
	if p.base != nil && p.summit != nil {
		k += fmt.Sprintf("_b%.0f_s%.0f", *p.base, *p.summit)
	}
	return k
}

func parseParams(c *gin.Context) (forecastParams, error) {
	var p forecastParams
	var err error
	if p.lat, err = strconv.ParseFloat(c.Query("lat"), 64); err != nil || p.lat < -90 || p.lat > 90 {
		return p, fmt.Errorf("lat must be a number between -90 and 90")
	}
	if p.lon, err = strconv.ParseFloat(c.Query("lon"), 64); err != nil || p.lon < -180 || p.lon > 180 {
		return p, fmt.Errorf("lon must be a number between -180 and 180")
	}
	p.name = c.Query("name")
	// kind is a GeoNames feature code; anything else is ignored.
	if kind := strings.ToUpper(c.Query("kind")); len(kind) <= 6 && strings.Trim(kind, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") == "" {
		p.kind = kind
	}
	for name, dst := range map[string]**float64{"elevation": &p.elevation, "base": &p.base, "summit": &p.summit} {
		raw := c.Query(name)
		if raw == "" {
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < -500 || v > 9000 {
			return p, fmt.Errorf("%s must be an elevation in metres", name)
		}
		*dst = &v
	}
	if (p.base == nil) != (p.summit == nil) {
		return p, fmt.Errorf("base and summit must be given together")
	}
	if p.base != nil && *p.base >= *p.summit {
		return p, fmt.Errorf("base must be below summit")
	}
	return p, nil
}

func (s *Server) forecast(c *gin.Context) {
	p, err := parseParams(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	key := p.key()
	if e, ok := s.Hot.Get(key, s.Now()); ok {
		c.Header("X-Cache", "hit")
		c.Data(http.StatusOK, "application/json; charset=utf-8", e.Body)
		return
	}
	// Concurrent requests for the same place share one upstream fetch. The
	// fetch is detached from this request so one client hanging up doesn't
	// fail the others.
	body, err, _ := s.flights.Do(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		return s.build(ctx, key, p)
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "forecast is unavailable: " + err.Error()})
		return
	}
	c.Header("X-Cache", "miss")
	c.Data(http.StatusOK, "application/json; charset=utf-8", body.([]byte))
}

func (s *Server) build(ctx context.Context, key string, p forecastParams) ([]byte, error) {
	notes := []string{}

	// Terrain around the point decides the elevation bands.
	lats, lons := terrain.Ring(p.lat, p.lon)
	ring, err := s.Source.Elevations(ctx, lats, lons)
	if err != nil {
		ring = nil
		notes = append(notes, "Terrain lookup failed, so only one elevation is shown.")
	}

	var pointElev float64
	switch {
	case p.elevation != nil:
		pointElev = *p.elevation
	case len(ring) > 0:
		pointElev = ring[0]
	default:
		return nil, fmt.Errorf("could not determine the elevation of this point")
	}

	var bands []compute.Band
	if p.base != nil {
		bands = terrain.Explicit(*p.base, *p.summit)
	} else {
		var around []float64
		if len(ring) > 1 {
			around = ring[1:]
		}
		bands = terrain.Bands(p.kind, pointElev, around)
	}

	ens, err := s.Source.Ensemble(ctx, p.lat, p.lon, bands[0].Elevation)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	result := compute.Build(ens, bands, now)
	if len(result.Days) == 0 {
		return nil, fmt.Errorf("upstream returned no usable forecast steps")
	}

	name := p.name
	if name == "" {
		name = fmt.Sprintf("%.3f, %.3f", p.lat, p.lon)
	}
	body, err := json.Marshal(forecastResponse{
		Location: location{
			Name: name, Kind: p.kind, Lat: p.lat, Lon: p.lon, Elevation: pointElev,
			Timezone: ens.Timezone, UTCOffsetSeconds: ens.UTCOffsetSeconds,
		},
		GeneratedAt: now.UTC(),
		Source: sourceInfo{
			Provider:    ens.Provider,
			Attribution: "Weather data by Open-Meteo.com (CC BY 4.0)",
		},
		Bands:  bands,
		Notes:  notes,
		Result: result,
	})
	if err != nil {
		return nil, err
	}
	if old, replaced := s.Hot.Put(store.Entry{Key: key, Body: body, FetchedAt: now}); replaced {
		s.archive(old)
	}
	return body, nil
}

func (s *Server) archive(e store.Entry) {
	if s.Archive == nil {
		return
	}
	if err := s.Archive.Archive(context.Background(), e); err != nil {
		log.Printf("archive %s: %v", e.Key, err)
	}
}

// Janitor moves stale forecasts out of memory into the archive until ctx ends.
func (s *Server) Janitor(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, e := range s.Hot.Sweep(s.Now()) {
				s.archive(e)
			}
			s.Fields.Sweep(s.Now())
		}
	}
}

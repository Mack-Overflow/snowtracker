package httpapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"precip-calendar/api/internal/provider"
	"precip-calendar/api/internal/store"
)

// Map fields are fetched on a lattice of whole multiples of a step, in grids
// with an even origin and odd size. Snapping this hard means nearby map views
// ask for the same grid and share one cached upstream fetch, which matters
// because upstream bills one call per grid point.
const (
	fieldMaxSide   = 13
	fieldMaxPoints = 144
	fieldMaxLat    = 85.0
)

var fieldSteps = map[string]float64{"0.25": 0.25, "0.5": 0.5, "1": 1, "2": 2, "4": 4}

type fieldParams struct {
	model          string
	stepKey        string
	step           float64
	i0, j0, nx, ny int // origin (west, south) in steps; size in points
}

func (p fieldParams) key() string {
	return fmt.Sprintf("field_%s_%s_%d_%d_%d_%d", p.model, p.stepKey, p.i0, p.j0, p.nx, p.ny)
}

func parseFieldParams(c *gin.Context) (fieldParams, error) {
	p := fieldParams{model: c.DefaultQuery("model", provider.FieldMean), stepKey: c.Query("step")}
	if p.model != provider.FieldMean && p.model != provider.FieldRun {
		return p, fmt.Errorf("model must be mean or run")
	}
	var ok bool
	if p.step, ok = fieldSteps[p.stepKey]; !ok {
		return p, fmt.Errorf("step must be one of 0.25, 0.5, 1, 2, 4")
	}
	for name, dst := range map[string]*int{"i0": &p.i0, "j0": &p.j0, "nx": &p.nx, "ny": &p.ny} {
		v, err := strconv.Atoi(c.Query(name))
		if err != nil {
			return p, fmt.Errorf("%s must be an integer", name)
		}
		*dst = v
	}
	if p.i0%2 != 0 || p.j0%2 != 0 {
		return p, fmt.Errorf("i0 and j0 must be even")
	}
	for _, n := range []int{p.nx, p.ny} {
		if n < 3 || n > fieldMaxSide || n%2 == 0 {
			return p, fmt.Errorf("nx and ny must be odd, from 3 to %d", fieldMaxSide)
		}
	}
	if p.nx*p.ny > fieldMaxPoints {
		return p, fmt.Errorf("at most %d grid points", fieldMaxPoints)
	}
	south, north := float64(p.j0)*p.step, float64(p.j0+p.ny-1)*p.step
	west := float64(p.i0) * p.step
	if south < -fieldMaxLat || north > fieldMaxLat || west < -360 || west > 360 {
		return p, fmt.Errorf("grid is outside the map")
	}
	return p, nil
}

// coords lists the grid row by row, south to north, west to east.
func (p fieldParams) coords() (lats, lons []float64) {
	for j := 0; j < p.ny; j++ {
		for i := 0; i < p.nx; i++ {
			lats = append(lats, float64(p.j0+j)*p.step)
			lon := math.Mod(float64(p.i0+i)*p.step+540, 360) - 180
			lons = append(lons, lon)
		}
	}
	return lats, lons
}

func (s *Server) field(c *gin.Context) {
	p, err := parseFieldParams(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	key := p.key()
	c.Header("Cache-Control", "private, max-age=900")
	if e, ok := s.Fields.Get(key, s.Now()); ok {
		c.Header("X-Cache", "hit")
		c.Data(http.StatusOK, "application/octet-stream", e.Body)
		return
	}
	body, err, _ := s.flights.Do(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		lats, lons := p.coords()
		f, err := s.Source.Field(ctx, p.model, lats, lons)
		if err != nil {
			return nil, err
		}
		body := encodeField(p, f)
		s.Fields.Put(store.Entry{Key: key, Body: body, FetchedAt: s.Now()})
		return body, nil
	})
	if err != nil {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusBadGateway, gin.H{"error": "map data is unavailable: " + err.Error()})
		return
	}
	c.Header("X-Cache", "miss")
	c.Data(http.StatusOK, "application/octet-stream", body.([]byte))
}

const (
	fieldMagic   = "PCF1"
	fieldMissing = math.MinInt16
)

// encodeField packs a field for the browser, little-endian:
//
//	"PCF1"
//	uint16 nx, ny, steps, 0
//	float32 south, west, step        degrees
//	float64 start                    Unix seconds of step 0; steps are hourly
//	float32 elevation[ny*nx]         metres, south to north, west to east
//	int16   precip[steps][ny*nx]     hundredths of a mm per hour
//	int16   temp[steps][ny*nx]       hundredths of a °C at elevation
//
// Missing values are -32768. The 32-byte header keeps every array aligned.
func encodeField(p fieldParams, f *provider.Field) []byte {
	n := p.nx * p.ny
	var buf bytes.Buffer
	buf.Grow(32 + 4*n + 4*n*f.Steps)
	buf.WriteString(fieldMagic)
	le := binary.LittleEndian
	binary.Write(&buf, le, [4]uint16{uint16(p.nx), uint16(p.ny), uint16(f.Steps), 0})
	binary.Write(&buf, le, [3]float32{float32(float64(p.j0) * p.step), float32(float64(p.i0) * p.step), float32(p.step)})
	binary.Write(&buf, le, float64(f.Start.Unix()))
	elev := make([]float32, n)
	for i, e := range f.Elevation {
		elev[i] = float32(e)
	}
	binary.Write(&buf, le, elev)
	plane := make([]int16, n)
	for _, series := range [][][]float32{f.Precip, f.Temp} {
		for t := 0; t < f.Steps; t++ {
			for i := range plane {
				plane[i] = hundredths(series[i][t])
			}
			binary.Write(&buf, le, plane)
		}
	}
	return buf.Bytes()
}

func hundredths(v float32) int16 {
	if v != v {
		return fieldMissing
	}
	return int16(math.Round(math.Max(-32767, math.Min(32767, float64(v)*100))))
}

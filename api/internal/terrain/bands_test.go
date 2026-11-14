package terrain

import "testing"

func TestBands(t *testing.T) {
	peak := Bands("MT", 4392, []float64{1746, 1474, 2131, 1230})
	if len(peak) != 3 || peak[0].Elevation != 4392 || peak[1].Elevation != 2810 || peak[2].Elevation != 1230 {
		t.Errorf("peak bands = %+v", peak)
	}
	if peak[0].Modelled || !peak[1].Modelled {
		t.Errorf("only derived bands should be flagged modelled: %+v", peak)
	}

	town := Bands("PPL", 1035, []float64{1100, 2400, 3800, 1020})
	if len(town) != 3 || town[0].Elevation != 3800 || town[2].Elevation != 1035 || town[2].Label != "Town" {
		t.Errorf("town bands = %+v", town)
	}

	flat := Bands("PPLC", 35, []float64{20, 60, 110})
	if len(flat) != 1 || flat[0].Elevation != 35 {
		t.Errorf("flat bands = %+v", flat)
	}

	// An unlabelled high point is still treated as a summit.
	unnamed := Bands("", 3000, []float64{1500, 2200, 2990})
	if len(unnamed) != 3 || unnamed[0].Label != "Summit" {
		t.Errorf("unnamed bands = %+v", unnamed)
	}

	if none := Bands("PPL", 500, nil); len(none) != 1 {
		t.Errorf("no terrain samples should give one band: %+v", none)
	}
}

func TestRing(t *testing.T) {
	lats, lons := Ring(46.85, -121.76)
	if len(lats) != 81 || len(lons) != 81 || lats[0] != 46.85 {
		t.Errorf("ring size %d/%d", len(lats), len(lons))
	}
}

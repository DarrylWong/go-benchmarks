// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stats

import (
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input, expected []float64
	}{
		{[]float64{}, []float64{}},
		{[]float64{0, 1}, []float64{0, 1}},
		{[]float64{1, 2, 3}, []float64{0, 0.5, 1}},
		{[]float64{-1, 0, 1}, []float64{0, 0.5, 1}},
		{[]float64{-3, -2, -1}, []float64{0, 0.5, 1}},
		{[]float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, []float64{0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1}},
	}
	for i, test := range tests {
		if out := normalize(test.input); !reflect.DeepEqual(out, test.expected) {
			t.Errorf("[%d] normalizeInput(%v) = %v, expected %v", i, test.input, out, test.expected)
		}
	}
}

func TestMedianResolution(t *testing.T) {
	t.Parallel()
	tests := []struct {
		l, expected int
	}{
		{1, 10},
		{1000, 10},
		{2048, 11},
	}
	for i, test := range tests {
		if v := medianResolution(test.l); v != test.expected {
			t.Errorf("[%d] medianResolution(%v) = %v, expected %v", i, test.l, v, test.expected)
		}
	}
}

func TestEDMXGolden(t *testing.T) {
	// This test case is used in the paper.
	input := []float64{
		105.08333, 90.90000, 763.90000, 83.36667, 78.36667, 80.58333,
		76.36667, 210.98333, 78.00000, 77.51667, 83.01667, 89.23333,
		84.86667, 653.16667, 70.91667, 72.83333, 75.91667, 73.53333,
		548.86667, 66.23333, 73.45000, 66.96667, 71.11667, 68.31667,
		285.38333, 317.20000, 63.28333, 64.08333, 60.50000, 550.88333,
		399.68333, 75.90000, 115.35000, 78.93333, 88.68333, 475.53333,
		30.11667, 31.51667, 34.08333, 39.55000, 47.51667, 423.63333,
		52.55000, 50.21667, 61.41667, 56.61667, 64.41667, 742.30000,
		165.85000, 122.88333, 122.21667, 114.66667, 565.96667, 134.70000,
		141.16667, 160.78333, 168.48333, 458.65000, 513.28333, 154.36667,
		130.66667, 125.93333, 127.25000, 615.58333, 122.90000, 97.45000,
		122.76667, 115.10000, 111.95000, 442.78333, 113.83333, 116.11667,
		128.70000, 135.03333, 138.75000, 153.38333, 143.58333, 161.50000,
		168.11667, 152.25000, 147.11667, 163.91667, 161.10000, 146.95000,
		132.65000, 127.28333, 116.10000, 92.28333, 54.88333, 111.35000,
		114.98333, 110.98333, 1015.35000, 774.58333, 232.65000, 134.61667,
		130.25000, 98.66667, 102.40000, 184.86667, 258.76667, 70.33333,
		81.38333, 81.10000, 89.21667, 536.96667, 85.83333, 95.63333,
		76.10000, 94.38333, 73.25000, 346.70000, 65.38333, 84.73333,
		140.56667, 120.60000, 121.38333, 359.23333, 55.28333, 54.55000,
		52.18333, 56.20000, 112.11667, 208.53333, 49.40000, 49.06667,
		56.06667, 54.01667, 63.51667, 344.41667, 42.06667, 55.36667,
		55.96667, 55.85000, 56.30000, 46.56667, 49.25000, 43.90000,
		357.61667, 44.10000, 44.68333, 43.13333, 40.55000, 452.20000,
		47.06667, 40.00000, 42.35000, 48.36667, 44.86667, 48.51667,
		244.01667, 50.16667, 48.73333, 47.91667, 51.96667, 343.33333,
		35.25000, 45.33333, 46.86667, 48.78333}
	if v := EDMX(input, 24); v != 95 {
		t.Errorf("EDMX() = %v, expected 95", v)
	}
	if v := EDM(input, 24); v != 47 {
		t.Errorf("EDM() = %v, expected 47", v)
	}
}

package coordstore

import "testing"

func TestHeapInuseDelta(t *testing.T) {
	tests := []struct {
		name     string
		baseline uint64
		peak     uint64
		want     uint64
	}{
		{
			name:     "peak above baseline",
			baseline: 10,
			peak:     42,
			want:     32,
		},
		{
			name:     "peak equal baseline",
			baseline: 42,
			peak:     42,
			want:     0,
		},
		{
			name:     "baseline above peak",
			baseline: 42,
			peak:     10,
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := heapInuseDelta(tt.baseline, tt.peak); got != tt.want {
				t.Fatalf("heapInuseDelta(%d, %d) = %d, want %d", tt.baseline, tt.peak, got, tt.want)
			}
		})
	}
}

package mono

import (
	"math"
	"testing"
)

func TestSignedMinorUnits(t *testing.T) {
	for _, tt := range []struct {
		n         int64
		direction string
		want      int64
	}{{500000, "debit", -500000}, {-500000, "debit", -500000}, {-500000, "credit", 500000}, {500000, "credit", 500000}, {500000, "", 500000}, {-500000, "", -500000}, {0, "debit", 0}} {
		got, err := SignedAmount(tt.n, tt.direction)
		if err != nil || got != tt.want {
			t.Fatal(tt, got, err)
		}
	}
	if _, err := SignedAmount(math.MinInt64, "credit"); err == nil {
		t.Fatal("overflow accepted")
	}
}

package usage

import "testing"

func TestCostNanosUsesPerMillionPrices(t *testing.T) {
	got := CostNanos(1_000_000, 500_000, CostRates{
		InputUSDPerMillion:  1.5,
		OutputUSDPerMillion: 4,
	})
	if got != 3_500_000_000 {
		t.Fatalf("CostNanos = %d, want 3500000000", got)
	}
}

func TestCostNanosRetainsSubMicroCharges(t *testing.T) {
	got := CostNanos(1, 0, CostRates{InputUSDPerMillion: 0.15})
	if got != 150 {
		t.Fatalf("CostNanos = %d, want 150", got)
	}
}

func TestGuardCountsInflightReservations(t *testing.T) {
	guard := NewGuard()
	limits := Limits{Session: 1000}
	load := func() (Totals, Totals, error) { return Totals{}, Totals{}, nil }
	first, err := guard.CheckAndReserve("session", "2026-11-11", 600, limits, false, load)
	if err != nil {
		t.Fatal(err)
	}
	if first.Scope != "" || first.Reservation == nil {
		t.Fatalf("first check = %+v", first)
	}
	second, err := guard.CheckAndReserve("session", "2026-11-11", 600, limits, false, load)
	if err != nil {
		t.Fatal(err)
	}
	if second.Scope != "session" || second.Used != 600 {
		t.Fatalf("second check = %+v", second)
	}
	first.Reservation.Release()
	third, err := guard.CheckAndReserve("session", "2026-11-11", 600, limits, false, load)
	if err != nil {
		t.Fatal(err)
	}
	if third.Scope != "" {
		t.Fatalf("third check = %+v", third)
	}
}

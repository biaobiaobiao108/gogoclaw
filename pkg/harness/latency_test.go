package harness

import "testing"

func TestSummarizeLatencyUsesNearestRankPercentiles(t *testing.T) {
	summary := summarizeLatency([]int64{50, 10, 40, 20, 30})
	if summary == nil || summary.P50MS != 30 || summary.P95MS != 50 {
		t.Fatalf("summary = %+v", summary)
	}
	if summarizeLatency(nil) != nil {
		t.Fatal("empty latency should return nil")
	}
}

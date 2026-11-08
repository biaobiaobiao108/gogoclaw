package seahorse

import "testing"

func TestGenerateSummaryIDUnique(t *testing.T) {
	const count = 1000
	seen := make(map[string]struct{}, count)
	for range count {
		id, err := generateSummaryID()
		if err != nil {
			t.Fatalf("generateSummaryID() error = %v", err)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("generateSummaryID() returned duplicate %q", id)
		}
		seen[id] = struct{}{}
	}
}

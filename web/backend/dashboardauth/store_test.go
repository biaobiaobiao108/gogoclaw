package dashboardauth

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestSetPasswordIfUninitializedHasSingleWinner(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), DBFilename))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	const requests = 4
	type result struct {
		password string
		created  bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			password := fmt.Sprintf("password-%d", index)
			created, err := store.SetPasswordIfUninitialized(context.Background(), password)
			results <- result{password: password, created: created, err: err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	winner := ""
	for result := range results {
		if result.err != nil {
			t.Fatalf("SetPasswordIfUninitialized() error = %v", result.err)
		}
		if result.created {
			if winner != "" {
				t.Fatalf("multiple winners: %q and %q", winner, result.password)
			}
			winner = result.password
		}
	}
	if winner == "" {
		t.Fatal("no setup request won")
	}
	ok, err := store.VerifyPassword(context.Background(), winner)
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if !ok {
		t.Fatal("winning password was not stored")
	}
}

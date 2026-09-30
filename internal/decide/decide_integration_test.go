//go:build integration

package decide

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestDecideMergesAcrossBatches drives a request large enough to force several
// batches and checks every key is answered exactly once across them.
func TestDecideMergesAcrossBatches(t *testing.T) {
	var (
		mu       sync.Mutex
		seenKeys = map[string]int{}
		batches  int
		maxInOne int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)

			return
		}

		mu.Lock()
		batches++

		if len(req.Questions) > maxInOne {
			maxInOne = len(req.Questions)
		}

		for k := range req.Questions {
			seenKeys[k]++
		}

		mu.Unlock()

		answers := map[string]map[string]float64{}
		for k := range req.Questions {
			answers[k] = map[string]float64{"noul": 0.8}
		}

		if err := json.NewEncoder(w).Encode(map[string]any{"answers": answers, "usage": map[string]float64{"cost": 0.001}}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer srv.Close()

	// Each instruction is large enough that only a handful fit per batch.
	big := strings.Repeat("x", maxRequestChars/8)
	qs := make([]Question, 0, 40)
	for i := range 40 {
		qs = append(qs, Question{Key: fmt.Sprintf("q%d", i), Instructions: big})
	}

	c := NewClient(srv.URL, "key", "jev-latest")

	got, err := c.Decide(context.Background(), "state", qs)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 40 {
		t.Fatalf("answers = %d want 40", len(got))
	}

	mu.Lock()
	defer mu.Unlock()

	if batches < 2 {
		t.Fatalf("batches = %d want >= 2 for a large request", batches)
	}

	for k, n := range seenKeys {
		if n != 1 {
			t.Fatalf("key %s sent %d times want 1", k, n)
		}
	}

	if maxInOne > len(qs) {
		t.Fatalf("impossible batch size %d", maxInOne)
	}
}

// TestDecideSplitsRecursively forces a server that rejects anything above one
// question, proving the client halves a batch until each fits.
func TestDecideSplitsRecursively(t *testing.T) {
	var rejected, answered int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)

			return
		}

		if len(req.Questions) > 1 {
			rejected++

			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"max_tokens_exceeded"}}`))

			return
		}

		answered++

		answers := map[string]map[string]float64{}
		for k := range req.Questions {
			answers[k] = map[string]float64{"noul": 0.6}
		}

		if err := json.NewEncoder(w).Encode(map[string]any{"answers": answers, "usage": map[string]float64{"cost": 0.0001}}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key", "jev-latest")
	qs := []Question{{Key: "a"}, {Key: "b"}, {Key: "c"}, {Key: "d"}}

	got, err := c.Decide(context.Background(), "state", qs)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 4 {
		t.Fatalf("answers = %v", got)
	}

	if rejected == 0 {
		t.Fatal("server never rejected a multi-question batch")
	}

	if answered != 4 {
		t.Fatalf("answered %d requests want 4 single-question requests", answered)
	}
}

// TestDecideSingleQuestionExceedingLimitErrors proves a lone over-limit
// question returns an error rather than looping.
func TestDecideSingleQuestionExceedingLimitErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"max_tokens_exceeded"}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key", "jev-latest")

	if _, err := c.Decide(context.Background(), "state", []Question{{Key: "solo"}}); err == nil {
		t.Fatal("want error for an over-limit single question")
	}
}

package decide

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecideSplitOnTokenLimit(t *testing.T) {
	var maxSeen, rejected int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)

			return
		}

		if len(req.Questions) > 2 {
			rejected++

			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"HTTP 400: {\"detail\":{\"error_type\":\"max_tokens_exceeded\"}}","code":400}}`))

			return
		}

		if len(req.Questions) > maxSeen {
			maxSeen = len(req.Questions)
		}

		answers := map[string]map[string]float64{}

		for k := range req.Questions {
			if k == "k3" {
				continue
			}

			answers[k] = map[string]float64{"noul": 0.7}
		}

		if err := json.NewEncoder(w).Encode(map[string]any{"answers": answers, "usage": map[string]float64{"cost": 0.001}}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key", "jev-latest")
	qs := []Question{{Key: "k1"}, {Key: "k2"}, {Key: "k3"}, {Key: "k4"}, {Key: "k5"}}

	got, err := c.Decide(context.Background(), "state", qs)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 5 {
		t.Fatalf("answers = %v", got)
	}

	if got["k3"] != 1.0 {
		t.Fatalf("missing key = %v want 1.0", got["k3"])
	}

	if rejected == 0 {
		t.Fatal("client never split a batch on max_tokens_exceeded")
	}

	if maxSeen > 2 {
		t.Fatalf("server answered a batch of %d questions", maxSeen)
	}

	if c.Cost() != 0.003 {
		t.Fatalf("cost = %v want 0.003", c.Cost())
	}
}

func TestDecideServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key", "jev-latest")

	_, err := c.Decide(context.Background(), "state", []Question{{Key: "k1"}})
	if err == nil {
		t.Fatal("want error")
	}

	// The error must name the failing status and surface the body, not just be
	// "some error" — otherwise a wrong error would pass unnoticed.
	if !strings.Contains(err.Error(), "decisions HTTP 500") {
		t.Fatalf("error = %q, want it to mention the status", err)
	}

	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %q, want it to include the response body", err)
	}
}

func TestDecideBadRequestWithoutTokenLimit(t *testing.T) {
	// A 400 that is NOT max_tokens_exceeded must surface as an error rather
	// than being split or retried.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad model"}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key", "jev-latest")

	_, err := c.Decide(context.Background(), "state", []Question{{Key: "k1"}, {Key: "k2"}})
	if err == nil || !strings.Contains(err.Error(), "decisions HTTP 400") {
		t.Fatalf("err = %v, want an HTTP 400 error", err)
	}
}

func TestBatchPacking(t *testing.T) {
	// Two questions whose combined size exceeds the budget must land in two
	// batches; small questions share one batch.
	big := strings.Repeat("x", maxRequestChars/2+1)
	qs := []Question{{Key: "a", Instructions: big}, {Key: "b", Instructions: big}}

	batches := batch("s", qs)
	if len(batches) != 2 {
		t.Fatalf("batches = %d want 2", len(batches))
	}

	small := []Question{{Key: "a"}, {Key: "b"}, {Key: "c"}}
	if got := batch("s", small); len(got) != 1 {
		t.Fatalf("small batches = %d want 1", len(got))
	}
}

func TestBatchSingleQuestionAlwaysFits(t *testing.T) {
	qs := []Question{{Key: "only", Instructions: strings.Repeat("x", MaxInstrChars)}}

	if got := batch(strings.Repeat("s", MaxStateChars), qs); len(got) != 1 {
		t.Fatalf("batches = %d want 1", len(got))
	}
}

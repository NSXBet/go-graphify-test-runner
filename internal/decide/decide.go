// Package decide is a SystemOne decisions client that answers keyed yes/no
// questions in token-bounded batches.
package decide

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// requestTimeout bounds a single decisions HTTP round trip.
const requestTimeout = 60 * time.Second

// Question-packing budget: the approximate per-question JSON overhead, the
// maximum state size, and the per-question instruction cap.
const (
	questionOverhead = 40
	// MaxStateChars caps the shared state text sent with every request.
	MaxStateChars = 30000
	// MaxInstrChars caps a single question's instruction text.
	MaxInstrChars = 1500
)

// maxRequestChars keeps the packed state + questions under the endpoint's
// token limit (~33k tokens at roughly 3 chars per token).
const maxRequestChars = 72000

type question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
}

type request struct {
	Model     string              `json:"model"`
	State     string              `json:"state"`
	Questions map[string]question `json:"questions"`
}

type response struct {
	ID       string `json:"id"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Answers  map[string]struct {
		Noul float64 `json:"noul"`
	} `json:"answers"`
	Usage struct {
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
		Cost         float64 `json:"cost"`
	} `json:"usage"`
}

// Question is one keyed yes/no question.
type Question struct {
	Key          string
	Instructions string
}

// Client is a SystemOne decisions client with request batching.
type Client struct {
	http     *http.Client
	endpoint string
	key      string
	model    string
	cost     float64
	verbose  io.Writer
}

// NewClient builds a client for the given endpoint, key and model.
func NewClient(endpoint, key, model string) *Client {
	return &Client{
		http:     &http.Client{Timeout: requestTimeout},
		endpoint: endpoint,
		key:      key,
		model:    model,
	}
}

// SetVerbose turns on the decisioning audit trail, written to w. Nil disables
// it (the default).
func (c *Client) SetVerbose(w io.Writer) { c.verbose = w }

// logf writes one audit line when verbose output is enabled.
func (c *Client) logf(format string, args ...any) {
	if c.verbose == nil {
		return
	}

	fmt.Fprintf(c.verbose, "[decide] "+format+"\n", args...)
}

// Cost returns the accumulated usage cost in dollars.
func (c *Client) Cost() float64 { return c.cost }

// Decide answers every keyed question, batching to stay under the request token
// limit and splitting batches on max_tokens_exceeded.
func (c *Client) Decide(ctx context.Context, state string, qs []Question) (map[string]float64, error) {
	answers := map[string]float64{}

	if c.verbose != nil {
		c.logf("state (%d chars):\n%s", len(state), state)

		for _, q := range qs {
			c.logf("question %q:\n%s", q.Key, q.Instructions)
		}
	}

	// ponytail: sequential batches; add a bounded worker pool if wall time matters
	for _, b := range batch(state, qs) {
		if err := c.decideBatch(ctx, state, b, answers); err != nil {
			return nil, err
		}
	}

	return answers, nil
}

// batch greedily packs questions into request-sized groups.
func batch(state string, qs []Question) [][]Question {
	var batches [][]Question

	cur := make([]Question, 0, len(qs))
	size := len(state)

	for _, q := range qs {
		cost := len(q.Key) + len(q.Instructions) + questionOverhead
		if len(cur) > 0 && size+cost > maxRequestChars {
			batches = append(batches, cur)

			cur = make([]Question, 0, len(qs))
			size = len(state)
		}

		cur = append(cur, q)
		size += cost
	}

	if len(cur) > 0 {
		batches = append(batches, cur)
	}

	return batches
}

func (c *Client) decideBatch(ctx context.Context, state string, batch []Question, out map[string]float64) error {
	keys := make([]string, 0, len(batch))
	for _, q := range batch {
		keys = append(keys, q.Key)
	}

	c.logf("POST %s model=%s state=%d chars questions=%d %v", c.endpoint, c.model, len(state), len(batch), keys)

	body, code, err := c.post(ctx, state, batch)
	if err != nil {
		return err
	}

	if code == http.StatusBadRequest && strings.Contains(string(body), "max_tokens_exceeded") {
		c.logf("HTTP 400 max_tokens_exceeded — splitting batch of %d", len(batch))

		return c.splitAndRetry(ctx, state, batch, out)
	}

	if code < 200 || code >= 300 {
		return fmt.Errorf("decisions HTTP %d: %s", code, body)
	}

	c.logf("HTTP %d: %s", code, strings.TrimSpace(string(body)))

	return c.collect(body, batch, out)
}

// splitAndRetry halves an over-limit batch and retries each half.
func (c *Client) splitAndRetry(ctx context.Context, state string, batch []Question, out map[string]float64) error {
	if len(batch) <= 1 {
		return fmt.Errorf("decisions HTTP %d: batch of one exceeds the token limit", http.StatusBadRequest)
	}

	mid := len(batch) / 2
	if err := c.decideBatch(ctx, state, batch[:mid], out); err != nil {
		return err
	}

	return c.decideBatch(ctx, state, batch[mid:], out)
}

// post sends one batch and returns the body and status code.
func (c *Client) post(ctx context.Context, state string, batch []Question) (body []byte, code int, err error) {
	reqBody := request{Model: c.model, State: state, Questions: make(map[string]question, len(batch))}
	for _, q := range batch {
		reqBody.Questions[q.Key] = question{Type: "noul", Instructions: q.Instructions}
	}

	buf, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}

	defer func() { _ = resp.Body.Close() }()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}

	return body, resp.StatusCode, nil
}

// collect records each answer, defaulting a missing key to run.
func (c *Client) collect(body []byte, batch []Question, out map[string]float64) error {
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("decode decisions response: %w", err)
	}

	c.cost += r.Usage.Cost

	c.logf("model=%s provider=%s id=%s tokens in/out=%d/%d cost=$%.6f",
		r.Model, r.Provider, r.ID, r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.Cost)

	for _, q := range batch {
		a, ok := r.Answers[q.Key]
		if !ok {
			fmt.Fprintf(os.Stderr, "warning: no answer for %s, running it\n", q.Key)
			c.logf("  %-40s <missing> -> 1.00 (default: run)", q.Key)

			out[q.Key] = 1.0

			continue
		}

		c.logf("  %-40s noul=%.4f", q.Key, a.Noul)
		out[q.Key] = a.Noul
	}

	return nil
}

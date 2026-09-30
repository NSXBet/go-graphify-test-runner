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
	Answers map[string]struct {
		Noul float64 `json:"noul"`
	} `json:"answers"`
	Usage struct {
		Cost float64 `json:"cost"`
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
}

// NewClient builds a client for the given endpoint, key and model.
func NewClient(endpoint, key, model string) *Client {
	return &Client{
		http:     &http.Client{Timeout: 60 * time.Second},
		endpoint: endpoint,
		key:      key,
		model:    model,
	}
}

// Cost returns the accumulated usage cost in dollars.
func (c *Client) Cost() float64 { return c.cost }

const (
	maxRequestChars = 72000
	maxStateChars   = 30000
	MaxInstrChars   = 1500
)

// Decide answers every keyed question, batching to stay under the request token
// limit and splitting batches on max_tokens_exceeded.
func (c *Client) Decide(ctx context.Context, state string, qs []Question) (map[string]float64, error) {
	answers := map[string]float64{}
	var batches [][]Question
	var cur []Question
	size := len(state)
	for _, q := range qs {
		cost := len(q.Key) + len(q.Instructions) + 40
		if len(cur) > 0 && size+cost > maxRequestChars {
			batches = append(batches, cur)
			cur = nil
			size = len(state)
		}
		cur = append(cur, q)
		size += cost
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}

	// ponytail: sequential batches; add a bounded worker pool if wall time matters
	for _, b := range batches {
		if err := c.decideBatch(ctx, state, b, answers); err != nil {
			return nil, err
		}
	}
	return answers, nil
}

func (c *Client) decideBatch(ctx context.Context, state string, batch []Question, out map[string]float64) error {
	reqBody := request{Model: c.model, State: state, Questions: map[string]question{}}
	for _, q := range batch {
		reqBody.Questions[q.Key] = question{Type: "noul", Instructions: q.Instructions}
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusBadRequest && strings.Contains(string(body), "max_tokens_exceeded") {
		if len(batch) > 1 {
			mid := len(batch) / 2
			if err := c.decideBatch(ctx, state, batch[:mid], out); err != nil {
				return err
			}
			return c.decideBatch(ctx, state, batch[mid:], out)
		}
		return fmt.Errorf("decisions HTTP %d: %s", resp.StatusCode, body)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("decisions HTTP %d: %s", resp.StatusCode, body)
	}

	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("decode decisions response: %w", err)
	}
	c.cost += r.Usage.Cost
	for _, q := range batch {
		a, ok := r.Answers[q.Key]
		if !ok {
			fmt.Fprintf(os.Stderr, "warning: no answer for %s, running it\n", q.Key)
			out[q.Key] = 1.0
			continue
		}
		out[q.Key] = a.Noul
	}
	return nil
}

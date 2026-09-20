// Workflow batch acquisition D01: URL input -> collector -> hasil/progress terstruktur.
// Peran: CLI tidak mengimplementasikan parsing/download sendiri; concurrency dibatasi di sini.
// Integrasi: collector lokal menghasilkan inventory, belum job/registry/snapshot produksi.
// Performa: deduplikasi URL dalam batch, worker bounded, pembatalan, dan hitungan kegagalan eksplisit.
package workflows

import (
	"context"
	"errors"
	"regulagraph.local/server/internal/ingestion/sources"
	"strings"
	"sync"
)

type SourceCollector interface {
	Collect(context.Context, string) (sources.Result, error)
}
type CollectionEvent struct {
	URL    string         `json:"url"`
	Result sources.Result `json:"result"`
	Error  string         `json:"error,omitempty"`
}
type CollectionSummary struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Reused    int `json:"reused"`
	Failed    int `json:"failed"`
}

func CollectSources(ctx context.Context, c SourceCollector, urls []string, workers int, report func(CollectionEvent)) (CollectionSummary, error) {
	if workers < 1 || workers > 16 {
		return CollectionSummary{}, errors.New("workers must be between 1 and 16")
	}
	unique := []string{}
	seen := map[string]bool{}
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u != "" && !seen[u] {
			seen[u] = true
			unique = append(unique, u)
		}
	}
	if len(unique) == 0 {
		return CollectionSummary{}, errors.New("no source URLs supplied")
	}
	jobs := make(chan string)
	events := make(chan CollectionEvent)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range jobs {
				r, e := c.Collect(ctx, u)
				event := CollectionEvent{URL: u, Result: r}
				if e != nil {
					event.Error = e.Error()
				}
				events <- event
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, u := range unique {
			select {
			case <-ctx.Done():
				return
			case jobs <- u:
			}
		}
	}()
	go func() { wg.Wait(); close(events) }()
	summary := CollectionSummary{Total: len(unique)}
	received := 0
	for event := range events {
		received++
		if event.Error != "" {
			summary.Failed++
		} else {
			summary.Completed++
			if event.Result.Reused {
				summary.Reused++
			}
		}
		if report != nil {
			report(event)
		}
	}
	summary.Failed += summary.Total - received
	if summary.Failed > 0 {
		return summary, errors.New("one or more documents failed or were cancelled; inspect records and retry")
	}
	return summary, nil
}

// Discovery workflow builds a persistent, deduplicated URL queue without downloading PDFs.
// Integration: single writer per output directory; checkpoint contains listing provenance and frontier.
// Performance: breadth-first seeds, bounded new pages/run, atomic page checkpoints, resume without refetch.
// Measure unique URLs/page, request errors and elapsed time; discovered URLs are not validated PDF counts.
package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regulagraph.local/server/internal/ingestion/sources"
	"strings"
)

type ListingReader interface {
	ReadListing(context.Context, string) (sources.ListingObservation, error)
}
type DiscoveryState struct {
	SchemaVersion int                                   `json:"schema_version"`
	Seeds         []string                              `json:"seeds"`
	Pages         map[string]sources.ListingObservation `json:"pages"`
	URLs          []string                              `json:"urls"`
	Errors        map[string]string                     `json:"errors"`
}

func persistDiscovery(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-discovery-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func DiscoverSources(ctx context.Context, reader ListingReader, seeds []string, dir string, maxPages int, report func(string, int, error)) (DiscoveryState, error) {
	state := DiscoveryState{SchemaVersion: 1, Pages: map[string]sources.ListingObservation{}, Errors: map[string]string{}}
	if maxPages < 1 || dir == "" {
		return state, errors.New("positive page limit and output directory required")
	}
	path := filepath.Join(dir, "discovery.json")
	if b, err := os.ReadFile(path); err == nil {
		if err = json.Unmarshal(b, &state); err != nil {
			return state, err
		}
		if state.SchemaVersion != 1 || state.Pages == nil || state.Errors == nil {
			return state, errors.New("unsupported discovery checkpoint")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return state, err
	}
	seenSeeds := map[string]bool{}
	for _, s := range state.Seeds {
		seenSeeds[s] = true
	}
	for _, s := range seeds {
		if !seenSeeds[s] {
			state.Seeds = append(state.Seeds, s)
			seenSeeds[s] = true
		}
	}
	if len(state.Seeds) == 0 {
		return state, errors.New("at least one listing seed required")
	}
	seenDocs := map[string]bool{}
	for _, u := range state.URLs {
		seenDocs[u] = true
	}
	save := func() error {
		b, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		if err = persistDiscovery(path, append(b, '\n')); err != nil {
			return err
		}
		return persistDiscovery(filepath.Join(dir, "queue.txt"), []byte("# Discovered detail URLs; PDF availability and canonical identity are not yet verified.\n"+strings.Join(state.URLs, "\n")+"\n"))
	}
	queue := append([]string(nil), state.Seeds...)
	visited := map[string]bool{}
	fetched := 0
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return state, err
		}
		u := queue[0]
		queue = queue[1:]
		if visited[u] {
			continue
		}
		visited[u] = true
		obs, cached := state.Pages[u]
		if !cached {
			if fetched >= maxPages {
				continue
			}
			fetched++
			var err error
			obs, err = reader.ReadListing(ctx, u)
			if err != nil {
				state.Errors[u] = err.Error()
				if e := save(); e != nil {
					return state, e
				}
				if report != nil {
					report(u, len(state.URLs), err)
				}
				continue
			}
			state.Pages[u] = obs
			delete(state.Errors, u)
			for _, d := range obs.Page.DocumentURLs {
				if !seenDocs[d] {
					seenDocs[d] = true
					state.URLs = append(state.URLs, d)
				}
			}
			if err = save(); err != nil {
				return state, err
			}
			if report != nil {
				report(u, len(state.URLs), nil)
			}
		}
		queue = append(queue, obs.Page.NextURLs...)
	}
	// Rebuild the text export even after a crash between checkpoint and export writes.
	if err := save(); err != nil {
		return state, err
	}
	if len(state.Errors) > 0 {
		return state, fmt.Errorf("%d listing errors remain; saved queue is usable, coverage incomplete", len(state.Errors))
	}
	return state, nil
}

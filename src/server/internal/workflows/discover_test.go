// Discovery tests verify checkpoint recovery, URL deduplication, failed-page retries and bounded work.
// Fake listing readers cannot download PDFs; live coverage remains a separate acquisition observation.
package workflows

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regulagraph.local/server/internal/ingestion/sources"
	"strings"
	"testing"
)

type listingFunc func(context.Context, string) (sources.ListingObservation, error)

func (f listingFunc) ReadListing(ctx context.Context, u string) (sources.ListingObservation, error) {
	return f(ctx, u)
}
func TestDiscoveryResumeRetryAndExport(t *testing.T) {
	dir := t.TempDir()
	calls := map[string]int{}
	reader := listingFunc(func(ctx context.Context, u string) (sources.ListingObservation, error) {
		calls[u]++
		obs := sources.ListingObservation{URL: u}
		if u == "first" {
			obs.Page.DocumentURLs = []string{"doc1"}
			obs.Page.NextURLs = []string{"second"}
		} else {
			if calls[u] == 1 {
				return obs, errors.New("transient")
			}
			obs.Page.DocumentURLs = []string{"doc1", "doc2"}
		}
		return obs, nil
	})
	s, e := DiscoverSources(context.Background(), reader, []string{"first"}, dir, 1, nil)
	if e != nil || len(s.URLs) != 1 || calls["second"] != 0 {
		t.Fatalf("bounded: %+v %v", s, e)
	}
	s, e = DiscoverSources(context.Background(), reader, nil, dir, 1, nil)
	if e == nil || len(s.Errors) != 1 || calls["first"] != 1 {
		t.Fatalf("failure: %+v %v", s, e)
	}
	s, e = DiscoverSources(context.Background(), reader, nil, dir, 1, nil)
	if e != nil || len(s.URLs) != 2 || len(s.Errors) != 0 || calls["first"] != 1 {
		t.Fatalf("retry: %+v %v", s, e)
	}
	if e = os.Remove(filepath.Join(dir, "queue.txt")); e != nil {
		t.Fatal(e)
	}
	_, e = DiscoverSources(context.Background(), reader, nil, dir, 1, nil)
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "queue.txt"))
	if e != nil || strings.Count(string(b), "doc1") != 1 || !strings.Contains(string(b), "doc2") || calls["second"] != 2 {
		t.Fatalf("export %s %v", b, e)
	}
}
func TestDiscoveryRejectsCorruptCheckpoint(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "discovery.json"), []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	_, e := DiscoverSources(context.Background(), listingFunc(func(context.Context, string) (sources.ListingObservation, error) {
		t.Fatal("unexpected fetch")
		return sources.ListingObservation{}, nil
	}), []string{"seed"}, dir, 1, nil)
	if e == nil {
		t.Fatal("corrupt checkpoint silently replaced")
	}
}

// Tests batch acquisition orchestration: duplicate URL, partial failures, and cancelled work.
// Peran: memastikan summary tidak menghilangkan kegagalan; tidak menguji akurasi model.
package workflows

import (
	"context"
	"errors"
	"regulagraph.local/server/internal/ingestion/sources"
	"testing"
)

type collectorFunc func(context.Context, string) (sources.Result, error)

func (f collectorFunc) Collect(ctx context.Context, u string) (sources.Result, error) {
	return f(ctx, u)
}
func TestCollectSourcesCountsDuplicatesAndFailures(t *testing.T) {
	fake := collectorFunc(func(ctx context.Context, u string) (sources.Result, error) {
		if u == "bad" {
			return sources.Result{}, errors.New("download failed")
		}
		return sources.Result{Reused: true}, nil
	})
	events := 0
	s, e := CollectSources(context.Background(), fake, []string{"good", "bad", "good"}, 2, func(CollectionEvent) { events++ })
	if e == nil || s.Total != 2 || s.Completed != 1 || s.Failed != 1 || s.Reused != 1 || events != 2 {
		t.Fatalf("summary %+v events %d err %v", s, events, e)
	}
}
func TestCancelledBatchDoesNotReportSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := collectorFunc(func(ctx context.Context, u string) (sources.Result, error) { return sources.Result{}, ctx.Err() })
	s, e := CollectSources(ctx, fake, []string{"one", "two"}, 1, nil)
	if e == nil || s.Failed != 2 || s.Completed != 0 {
		t.Fatalf("cancelled summary: %+v %v", s, e)
	}
}

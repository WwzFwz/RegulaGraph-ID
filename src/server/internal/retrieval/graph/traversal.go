// Menelusuri relasi terarah dari seed yang berasal dari linking atau pencarian teks.
//
// Peran dalam komponen:
// Mengambil bukti lintas pasal/dokumen yang mungkin tidak mirip secara lexical.
//
// Kontrak integrasi dan perhatian implementasi:
// Pertahankan predicate, versi, dan arah; hop, fan-out, serta budget kandidat konfigurabel dan truncation terlapor.
//
// Benchmark dan gate penerimaan:
// [RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.
//
// [GRAPH] Ukur validitas endpoint dan provenance, ketepatan predicate/arah, kelengkapan jalur bukti, waktu assembly/traversal, dan penggunaan memori. Gate: graph yang dipublikasikan tidak memiliki endpoint/bukti wajib yang hilang. Connectivity adalah diagnosis, bukan target memaksa satu komponen.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: discovery traversal snapshot-bound aktif melalui port neighborhood batch.
// Jalur node-simple mempertahankan alternatif serta arah assertion asli; batas hop,
// path, assertion/support dan byte dilaporkan. Legal-time/source-text acceptance
// masih milik hydration; keluaran ini belum Evidence yang layak untuk generation.
// Bukti verifikasi: Test dense hubs, cycles, missing supports and cancellation; measure path completeness vs p95/p99, report budget exhaustion.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package graph

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type NeighborhoodReader interface {
	ReadNeighborhood(context.Context, []string, domain.GraphReadLimits) (*domain.GraphNeighborhood, error)
}

type TraversalConfig struct {
	MaximumHops  int
	MaximumPaths int
	Read         domain.GraphReadLimits
}

// Validate also runs during preparation, so an empty seed result cannot hide
// invalid traversal limits until a later question happens to match an entity.
func (config TraversalConfig) Validate() error {
	if config.MaximumHops < 1 || config.MaximumHops > 16 || config.MaximumPaths < 1 || config.MaximumPaths > 4096 || config.Read.Assertions < 1 || config.Read.Assertions > 128 || config.Read.Supports < 1 || config.Read.Supports > 256 || config.Read.Bytes < 1 || config.Read.Bytes > 16<<20 {
		return errors.New("bounded graph traversal configuration required")
	}
	return nil
}

type TraversalResult struct {
	Snapshot          *pb.SnapshotRef
	Paths             []*pb.GraphPath
	Assertions        map[string]*pb.RelationAssertion
	Supports          map[string]*pb.SupportRecord
	Entities          map[string]*pb.CanonicalEntity
	FrontierExhausted bool
	StopReasons       []string
	ReadBytes         uint64
	ReadBatches       int
}

// Traverse explores both directions for discovery. An assertion's subject/object
// are never swapped; ordered path nodes describe the walk, not predicate direction.
// Seeds must already be linked under the same authorized snapshot. Deadline comes
// from the request/pin; no timer goroutine or model call is created here.
func Traverse(ctx context.Context, reader NeighborhoodReader, snapshot *pb.SnapshotRef, seeds []string, config TraversalConfig) (*TraversalResult, error) {
	if ctx == nil || reader == nil || snapshot == nil || len(seeds) < 1 || len(seeds) > 64 || config.Validate() != nil {
		return nil, errors.New("bounded traversal configuration and snapshot required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("traversal requires request deadline")
	}
	if err := domain.ValidateWire(snapshot, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	seeds = append([]string(nil), seeds...)
	sort.Strings(seeds)
	for i, id := range seeds {
		if i > 0 && seeds[i-1] == id {
			return nil, errors.New("duplicate graph seed")
		}
		if err := domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: snapshot.CorpusId, RecordId: id}, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	out := &TraversalResult{Snapshot: proto.Clone(snapshot).(*pb.SnapshotRef), Assertions: map[string]*pb.RelationAssertion{}, Supports: map[string]*pb.SupportRecord{}, Entities: map[string]*pb.CanonicalEntity{}}
	mark := func(reason string) {
		for _, existing := range out.StopReasons {
			if existing == reason {
				return
			}
		}
		out.StopReasons = append(out.StopReasons, reason)
	}
	frontier := make([]*pb.GraphPath, 0, len(seeds))
	for _, id := range seeds {
		frontier = append(frontier, &pb.GraphPath{OrderedNodeIds: []string{id}})
	}
	for hop := 0; hop < config.MaximumHops && len(frontier) != 0; hop++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		unique := map[string]bool{}
		for _, path := range frontier {
			unique[path.OrderedNodeIds[len(path.OrderedNodeIds)-1]] = true
		}
		nodes := make([]string, 0, len(unique))
		for id := range unique {
			nodes = append(nodes, id)
		}
		sort.Strings(nodes)
		adjacent := map[string][]*pb.RelationAssertion{}
		for start := 0; start < len(nodes); start += 64 {
			if out.ReadBytes >= config.Read.Bytes {
				mark("read_budget")
				return finishTraversal(out), nil
			}
			limits := config.Read
			limits.Bytes -= out.ReadBytes
			batchSeeds := nodes[start:min(start+64, len(nodes))]
			batch, err := reader.ReadNeighborhood(ctx, batchSeeds, limits)
			out.ReadBatches++
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if errors.Is(err, domain.ErrGraphReadBudget) {
					mark("read_budget")
					return finishTraversal(out), nil
				}
				return nil, err
			}
			if err = admitNeighborhood(out, batch, batchSeeds, limits); err != nil {
				return nil, err
			}
			if batch.MoreAssertions {
				mark("assertion_budget")
			}
			selected := map[string]bool{}
			for _, id := range batchSeeds {
				selected[id] = true
			}
			for _, returned := range batch.Assertions {
				a := out.Assertions[returned.Meta.RecordId]
				if selected[a.SubjectId] {
					adjacent[a.SubjectId] = append(adjacent[a.SubjectId], a)
				}
				if a.ObjectId != a.SubjectId && selected[a.ObjectId] {
					adjacent[a.ObjectId] = append(adjacent[a.ObjectId], a)
				}
			}
		}
		for id := range adjacent {
			sort.Slice(adjacent[id], func(i, j int) bool { return adjacent[id][i].Meta.RecordId < adjacent[id][j].Meta.RecordId })
		}
		supportIDs := map[string][]string{}
		for id, s := range out.Supports {
			supportIDs[s.AssertionId] = append(supportIDs[s.AssertionId], id)
		}
		for id := range supportIDs {
			sort.Strings(supportIDs[id])
		}
		var next []*pb.GraphPath
		for _, path := range frontier {
			last := path.OrderedNodeIds[len(path.OrderedNodeIds)-1]
			for _, a := range adjacent[last] {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				target := a.ObjectId
				if target == last {
					target = a.SubjectId
				}
				cycle := false
				for _, id := range path.OrderedNodeIds {
					cycle = cycle || id == target
				}
				if cycle {
					continue
				}
				if len(out.Paths) >= config.MaximumPaths {
					mark("path_budget")
					return finishTraversal(out), nil
				}
				p := proto.Clone(path).(*pb.GraphPath)
				p.OrderedNodeIds = append(p.OrderedNodeIds, target)
				p.OrderedAssertionIds = append(p.OrderedAssertionIds, a.Meta.RecordId)
				// C01 GraphPath has exactly one selected support per edge. Keep
				// every alternate support in out.Supports for source hydration;
				// this lexical choice is reproducibility, not a quality ranking.
				p.SelectedSupportIds = append(p.SelectedSupportIds, supportIDs[a.Meta.RecordId][0])
				p.Snapshot = proto.Clone(snapshot).(*pb.SnapshotRef)
				p.Coverage = pb.Completeness_COMPLETENESS_PARTIAL // Source/temporal hydration still required.
				identity, _ := json.Marshal([]any{snapshot, p.OrderedNodeIds, p.OrderedAssertionIds, p.SelectedSupportIds})
				p.PathId = fmt.Sprintf("path:%x", sha256.Sum256(identity))
				out.Paths = append(out.Paths, p)
				next = append(next, p)
			}
		}
		frontier = next
	}
	if len(frontier) != 0 {
		mark("hop_budget")
	}
	out.FrontierExhausted = len(frontier) == 0 && len(out.StopReasons) == 0
	return finishTraversal(out), nil
}

func finishTraversal(out *TraversalResult) *TraversalResult {
	for _, path := range out.Paths {
		path.FrontierExhausted = out.FrontierExhausted
	}
	return out
}

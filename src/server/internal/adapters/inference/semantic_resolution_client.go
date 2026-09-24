// Adapts one reusable gRPC Semantic client to the ingestion model-proposal workflow.
// The workflow owns deadlines, verified context hydration, manifest/result validation and
// durable replay; this adapter performs no implicit retries or registry writes. Measure
// client end-to-end p95/p99 including network/queue against required benchmark targets.
package inference

import (
	"context"
	"errors"
	"google.golang.org/grpc"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type SemanticResolutionClient struct{ client pb.SemanticClient }

func NewSemanticResolutionClient(connection grpc.ClientConnInterface) (*SemanticResolutionClient, error) {
	if connection == nil {
		return nil, errors.New("semantic gRPC connection is required")
	}
	return &SemanticResolutionClient{client: pb.NewSemanticClient(connection)}, nil
}
func (c *SemanticResolutionClient) ResolveBatch(ctx context.Context, request *pb.SemanticResolveRequest) (*pb.SemanticResolveResponse, error) {
	return c.client.ResolveBatch(ctx, request)
}

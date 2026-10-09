// Adapts shared transport reranking diagnostics to the operator CLI envelope.
// C01 model/scores and ordering checks live in api/schemas for CLI and HTTP reuse;
// this wrapper preserves the existing output format, not a ranking algorithm.
package main

import (
	"regulagraph.local/server/internal/api/schemas"
	"regulagraph.local/server/internal/workflows"
)

type queryRerankJSON = schemas.Reranking

func queryRerankOutput(result *workflows.RAGResult, required bool) (*queryRerankJSON, error) {
	return schemas.EncodeReranking(result, required)
}

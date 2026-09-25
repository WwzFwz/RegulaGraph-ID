// Wires the optional Go RESOLVE proposal executor to one reusable loopback gRPC client.
// Operator-pinned producer/schema/policy files are loaded once; the daemon never creates
// review approvals. Missing pins or incompatible budgets fail startup. Measure scheduler
// fairness, queue/model latency and memory under benchmark-targets.yaml; targets unmeasured.
package main

import (
	"errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"os"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	serverconfig "regulagraph.local/server/internal/config"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

func resolutionConfig(base runtimeConfig, ontology *domain.Ontology) (workflows.SemanticExecutorConfig, string, bool, error) {
	var result workflows.SemanticExecutorConfig
	enabled := os.Getenv("REGULAGRAPH_RESOLUTION_ENABLED")
	if enabled == "" || enabled == "false" {
		return result, "", false, nil
	}
	if enabled != "true" {
		return result, "", false, errors.New("REGULAGRAPH_RESOLUTION_ENABLED must be true or false")
	}
	endpoint := os.Getenv("REGULAGRAPH_RESOLUTION_ENDPOINT")
	if err := requireLoopback(endpoint); err != nil {
		return result, "", false, err
	}
	producerHash := os.Getenv("REGULAGRAPH_RESOLUTION_PRODUCER_SHA256")
	producer, err := serverconfig.LoadResolutionProducer(os.Getenv("REGULAGRAPH_RESOLUTION_PRODUCER_PATH"), producerHash)
	if err != nil {
		return result, "", false, err
	}
	schema, err := serverconfig.LoadResolutionOutputSchema(os.Getenv("REGULAGRAPH_WORKER_RESOLUTION_OUTPUT_SCHEMA"), os.Getenv("REGULAGRAPH_RESOLUTION_SCHEMA_SHA256"))
	if err != nil {
		return result, "", false, err
	}
	policies, err := serverconfig.LoadCandidatePlanningPolicies(os.Getenv("REGULAGRAPH_CANDIDATE_POLICY_PATH"), os.Getenv("REGULAGRAPH_CANDIDATE_POLICY_SHA256"))
	if err != nil {
		return result, "", false, err
	}
	result = workflows.SemanticExecutorConfig{OwnerID: base.ownerID, AuthScope: base.authScope, Lease: base.lease,
		CallTimeout: base.callTimeout, CancellationPoll: base.cancellationPoll, RetryBase: base.retryBase, RetryMax: base.retryMax,
		Ontology: ontology, Policies: policies, Producer: producer, ProducerPin: &pb.ContentHash{Sha256: producerHash}, OutputSchema: schema}
	values := []struct {
		name     string
		fallback int
		target   *int
	}{
		{"REGULAGRAPH_RESOLUTION_MAX_REFERENCES", 100000, &result.MaximumReferences},
		{"REGULAGRAPH_RESOLUTION_MAX_CANDIDATES_PER_MENTION", 128, &result.MaximumCandidatesPerMention},
		{"REGULAGRAPH_RESOLUTION_MAX_SCOPES", 10000, &result.MaximumScopes},
		{"REGULAGRAPH_RESOLUTION_MAX_ALIASES_PER_SCOPE", 128, &result.MaximumAliasesPerScope}}
	for _, v := range values {
		*v.target, err = positiveIntEnv(v.name, v.fallback)
		if err != nil {
			return result, "", false, err
		}
	}
	maximum, err := positiveIntEnv("REGULAGRAPH_RESOLUTION_MAX_BYTES", 64<<20)
	if err != nil {
		return result, "", false, err
	}
	result.MaximumBytes = uint64(maximum)
	return result, endpoint, true, nil
}

func newResolutionExecutor(base runtimeConfig, ontology *domain.Ontology, repository *postgres.Repository,
	artifacts *storage.FileStore) (*workflows.SemanticExecutor, func(), error) {
	config, endpoint, enabled, err := resolutionConfig(base, ontology)
	if err != nil || !enabled {
		return nil, func() {}, err
	}
	connection, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(base.maxMessageBytes), grpc.MaxCallSendMsgSize(base.maxMessageBytes)))
	if err != nil {
		return nil, func() {}, err
	}
	closeClient := func() { _ = connection.Close() }
	client, err := inference.NewSemanticResolutionClient(connection)
	if err != nil {
		closeClient()
		return nil, func() {}, err
	}
	executor, err := workflows.NewSemanticExecutor(repository, artifacts, client, config)
	if err != nil {
		closeClient()
		return nil, func() {}, err
	}
	return executor, closeClient, nil
}

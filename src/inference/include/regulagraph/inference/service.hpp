// Owns the bounded native inference RPC service and shared GPU scheduling worker.
// Input/output use C01 generated messages. Sessions/tokenizers are supplied at startup;
// item correlation, cancellation and queue accounting survive internal batch regrouping.
// Measure client p95/p99 and mixed query/bulk throughput against benchmark-targets.yaml.
#pragma once
#include "regulagraph/inference/runtime.hpp"
#include "regulagraph/v1/inference.grpc.pb.h"
#include <memory>
namespace regulagraph::inference {
class InferenceService final: public v1::Inference::Service {
public:
    explicit InferenceService(std::vector<std::unique_ptr<ModelRuntime>> models);
    ~InferenceService() override;
    grpc::Status EmbedBatch(grpc::ServerContext*,const v1::EmbedBatchRequest*,v1::EmbedBatchResponse*) override;
    grpc::Status RerankBatch(grpc::ServerContext*,const v1::RerankBatchRequest*,v1::RerankBatchResponse*) override;
    grpc::Status GetCapabilities(grpc::ServerContext*,const v1::CapabilitiesRequest*,v1::CapabilitiesResponse*) override;
private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};
}

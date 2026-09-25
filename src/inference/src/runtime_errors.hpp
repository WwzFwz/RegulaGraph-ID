// Classifies ONNX execution termination without hiding concurrent device failures.
// Integration: only the pinned ORT 1.22 FAIL sentinel plus an actual cancellation
// request may become InferenceCancelled. All other failures retire service readiness.
// This constant-time classification preserves error-rate/load denominators; quality
// and performance gates remain in configs/benchmark-targets.yaml, not fixture results.
#pragma once
#include <onnxruntime_c_api.h>
#include <stdexcept>
#include <string_view>

namespace regulagraph::inference {
class InferenceCancelled final : public std::runtime_error {
public:
    InferenceCancelled() : std::runtime_error("inference cancelled") {}
};

inline bool ConfirmedTermination(OrtErrorCode code, std::string_view message, bool requested) {
    // onnxruntime v1.22.0/core/framework/stream_execution_context.cc: RunSince.
    return requested && code == ORT_FAIL &&
        message == "Exiting due to terminate flag being set to true.";
}
}

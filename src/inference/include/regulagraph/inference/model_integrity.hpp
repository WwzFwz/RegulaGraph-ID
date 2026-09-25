// Streams the pinned ONNX graph to verify every external tensor belongs to its hashed catalog.
// This guards startup integrity without allocating a second model-sized protobuf object.
// Supports the exporter profile; unsupported training/functions/sparse tensors fail closed.
// Cold-load cost is measured separately from warm MODEL gates in configs/benchmark-targets.yaml.
#pragma once
#include <filesystem>
namespace regulagraph::inference {
void VerifyOnnxDependencies(const std::filesystem::path& graph, bool has_sidecar);
}

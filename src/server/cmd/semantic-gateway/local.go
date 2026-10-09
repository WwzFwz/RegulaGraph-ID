// Loads explicit local semantic admission pins without network or file hashing.
// Runtime startup performs GGUF and resident-server admission; manifest export
// only fingerprints this policy. Exact counting adds bounded HTTP work per item;
// measure that overhead with extraction latency under benchmark-targets.yaml.
package main

import (
	"errors"
	"os"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
)

func loadLocalBinding(model *pb.ModelManifest) (*inference.LlamaModelBinding, error) {
	mode := os.Getenv("REGULAGRAPH_SEMANTIC_ADMISSION")
	path := os.Getenv("REGULAGRAPH_SEMANTIC_GGUF_PATH")
	build := os.Getenv("REGULAGRAPH_SEMANTIC_LLAMA_BUILD")
	template := os.Getenv("REGULAGRAPH_SEMANTIC_TEMPLATE_SHA256")
	if mode == "" || mode == "provider" {
		if path != "" || build != "" || template != "" {
			return nil, errors.New("local semantic pins require explicit llama.cpp admission")
		}
		return nil, nil
	}
	if mode != "llama.cpp" {
		return nil, errors.New("semantic admission must be provider or llama.cpp")
	}
	hash, err := requiredHashEnv("REGULAGRAPH_SEMANTIC_TEMPLATE_SHA256")
	if err != nil {
		return nil, err
	}
	binding := &inference.LlamaModelBinding{Model: model, GGUFPath: path, ServerBuild: build, TemplateHash: hash}
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	return binding, nil
}

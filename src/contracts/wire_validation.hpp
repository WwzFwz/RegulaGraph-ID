// Descriptor-based C++ boundary validation for generated C01 messages.
// Input/output: message plus resource limits -> exception on invalid fields/invariants.
// Parsing is size/recursion-bounded; no model or connection is opened. Profile before claiming latency gates.
#pragma once
#include <google/protobuf/message.h>
#include <cstddef>
#include <string>
namespace regulagraph::contracts {
struct Limits { std::size_t max_bytes=16*1024*1024; int max_depth=64; std::size_t max_items=100000; };
void validate(const google::protobuf::Message& message, Limits limits={});
void decode(const std::string& bytes, google::protobuf::Message& message, Limits limits={});
void check_utf8_span(const std::string& text, std::size_t start, std::size_t end);
}

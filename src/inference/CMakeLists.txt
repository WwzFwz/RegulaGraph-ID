# Build library scaffold inference native C++.
# Peran: memvalidasi layout header/source tanpa mengunduh ONNX Runtime atau model.
# Integrasi: target ini belum mengekspos layanan inference; linking runtime ditambahkan kelak.
# Benchmark: kompilasi berhasil bukan hasil benchmark latency atau kualitas model.
cmake_minimum_required(VERSION 3.20)
project(regulagraph_inference VERSION 0.0.1 LANGUAGES CXX)

add_library(regulagraph_inference STATIC
    src/runtime.cpp
    src/embeddings.cpp
    src/cross_encoder.cpp
    src/batching.cpp
)
target_compile_features(regulagraph_inference PUBLIC cxx_std_17)
target_include_directories(regulagraph_inference PUBLIC
    "${CMAKE_CURRENT_SOURCE_DIR}/include"
)

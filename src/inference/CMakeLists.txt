# Build library inference native dan tes scheduler tanpa mengunduh ONNX Runtime atau model.
# Integrasi: scheduler belum mengekspos layanan inference; linking runtime ditambahkan kelak.
# Benchmark: kompilasi dan tes deterministik bukan hasil latency atau kualitas model.
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

include(CTest)
if(BUILD_TESTING)
    add_executable(regulagraph_batching_test src/batching_test.cpp)
    target_link_libraries(regulagraph_batching_test PRIVATE regulagraph_inference)
    add_test(NAME regulagraph_batching COMMAND regulagraph_batching_test)
endif()

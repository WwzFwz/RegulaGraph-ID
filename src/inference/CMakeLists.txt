# Build library inference native dan tes scheduler tanpa mengunduh ONNX Runtime atau model.
# Integrasi: opsi MODEL_RUNTIME menghubungkan ONNX, tokenizer native, wire C01 dan gRPC.
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

option(REGULAGRAPH_MODEL_RUNTIME "Build the pinned ONNX embedding/reranking gRPC service" OFF)
if(REGULAGRAPH_MODEL_RUNTIME)
    set(REGULAGRAPH_ORT_ROOT "" CACHE PATH "ONNX Runtime 1.22.0 headers/import library/runtime")
    set(REGULAGRAPH_TOKENIZER_LIBRARY "" CACHE FILEPATH "Release Rust tokenizer static library")
    find_path(ORT_INCLUDE onnxruntime_cxx_api.h PATHS "${REGULAGRAPH_ORT_ROOT}/include" NO_DEFAULT_PATH REQUIRED)
    find_library(ORT_LIBRARY onnxruntime PATHS "${REGULAGRAPH_ORT_ROOT}/lib" NO_DEFAULT_PATH REQUIRED)
    if(NOT EXISTS "${REGULAGRAPH_TOKENIZER_LIBRARY}")
        message(FATAL_ERROR "Build regulagraph-inference-tokenizer --release and pass its static library")
    endif()
    find_package(gRPC 1.76.0 EXACT CONFIG REQUIRED)
    set(REGULAGRAPH_WIRE_TESTS OFF CACHE BOOL "Wire probe belongs to separate verification" FORCE)
    add_subdirectory("${CMAKE_CURRENT_SOURCE_DIR}/../contracts" "${CMAKE_CURRENT_BINARY_DIR}/wire")
    set(GRPC_GEN "${CMAKE_CURRENT_BINARY_DIR}/grpc-generated")
    file(MAKE_DIRECTORY "${GRPC_GEN}")
    set(INFERENCE_PROTO "${CMAKE_CURRENT_SOURCE_DIR}/../contracts/proto/regulagraph/v1/inference.proto")
    get_target_property(PROTOBUF_INCLUDE protobuf::libprotobuf INTERFACE_INCLUDE_DIRECTORIES)
    find_program(REGULAGRAPH_PROTOC protoc REQUIRED)
    add_custom_command(OUTPUT "${GRPC_GEN}/regulagraph/v1/inference.grpc.pb.cc" "${GRPC_GEN}/regulagraph/v1/inference.grpc.pb.h"
        COMMAND "${REGULAGRAPH_PROTOC}" "--proto_path=${CMAKE_CURRENT_SOURCE_DIR}/../contracts/proto"
        "--proto_path=${PROTOBUF_INCLUDE}" "--grpc_out=${GRPC_GEN}"
        "--plugin=protoc-gen-grpc=$<TARGET_FILE:gRPC::grpc_cpp_plugin>" "${INFERENCE_PROTO}"
        DEPENDS "${INFERENCE_PROTO}" VERBATIM)
    target_sources(regulagraph_inference PRIVATE src/service.cpp src/model_integrity.cpp "${GRPC_GEN}/regulagraph/v1/inference.grpc.pb.cc")
    target_compile_definitions(regulagraph_inference PUBLIC REGULAGRAPH_WITH_ONNX)
    target_include_directories(regulagraph_inference PUBLIC "${GRPC_GEN}" PRIVATE "${ORT_INCLUDE}")
    target_link_libraries(regulagraph_inference PUBLIC regulagraph_wire gRPC::grpc++
        "${ORT_LIBRARY}" "${REGULAGRAPH_TOKENIZER_LIBRARY}")
    if(WIN32)
        target_link_libraries(regulagraph_inference PUBLIC bcrypt ntdll userenv ws2_32)
    else()
        target_link_libraries(regulagraph_inference PUBLIC dl pthread m)
    endif()
    add_executable(regulagraph_inference_server src/main.cpp)
    target_link_libraries(regulagraph_inference_server PRIVATE regulagraph_inference)
    add_executable(regulagraph_token_probe src/token_probe.cpp)
    target_link_libraries(regulagraph_token_probe PRIVATE regulagraph_inference)
    if(WIN32)
        add_custom_command(TARGET regulagraph_inference_server POST_BUILD
            COMMAND ${CMAKE_COMMAND} -E copy_directory "${REGULAGRAPH_ORT_ROOT}/lib" "$<TARGET_FILE_DIR:regulagraph_inference_server>")
    endif()
endif()

include(CTest)
if(BUILD_TESTING)
    add_executable(regulagraph_batching_test src/batching_test.cpp)
    target_link_libraries(regulagraph_batching_test PRIVATE regulagraph_inference)
    add_test(NAME regulagraph_batching COMMAND regulagraph_batching_test)
    if(REGULAGRAPH_MODEL_RUNTIME)
        add_executable(regulagraph_runtime_errors_test src/runtime_errors_test.cpp)
        target_compile_features(regulagraph_runtime_errors_test PRIVATE cxx_std_17)
        target_include_directories(regulagraph_runtime_errors_test PRIVATE "${ORT_INCLUDE}")
        add_test(NAME regulagraph_runtime_errors COMMAND regulagraph_runtime_errors_test)
    endif()
endif()

// Lifecycle session inference dan pemilihan execution provider.
//
// Peran dalam komponen:
// Lokasi implementasi untuk header inference terkait.
//
// Integrasi dan perhatian performa:
// Model dimuat sekali per lifecycle; ukur cold load terpisah dari warm inference; ONNX Runtime 1.22.0 dan bundle model dipin saat bootstrap.
//
// Benchmark dan gate penerimaan:
// Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//
// Status: pinned ONNX sessions and native tokenization active behind REGULAGRAPH_MODEL_RUNTIME.
// Perhatian implementasi dan verifikasi:
// Own long-lived model sessions, tokenizer/config manifests, resource pools and explicit startup/shutdown; connect the C01 wire library during N01.
// Bukti verifikasi: Measure cold start separately; test load failure cleanup, concurrent reuse and cancellation without per-request reload.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

#include "regulagraph/inference/runtime.hpp"
#ifdef REGULAGRAPH_WITH_ONNX
#include "wire_validation.hpp"
#include "regulagraph/inference/model_integrity.hpp"
#define ORT_API_MANUAL_INIT
#include <onnxruntime_cxx_api.h>
#include <google/protobuf/util/json_util.h>
#include <google/protobuf/struct.pb.h>
#include <algorithm>
#include <array>
#include <condition_variable>
#include <fstream>
#include <mutex>
#include <limits>
#include <stdexcept>
#include <thread>

extern "C" {
int rg_tokenizer_load(const unsigned char*,std::size_t,void**);
void rg_tokenizer_free(void*);
int rg_encode(const void*,const unsigned char*,std::size_t,const unsigned char*,std::size_t,void**);
std::size_t rg_encoding_len(const void*);
const std::uint32_t* rg_encoding_ids(const void*);
const std::uint32_t* rg_encoding_types(const void*);
void rg_encoding_free(void*);
int rg_sha256(const unsigned char*,std::size_t,unsigned char*);
int rg_sha256_file(const unsigned char*,std::size_t,unsigned char*);
}

namespace regulagraph::inference {
namespace {
void Need(bool value,const char* message) { if (!value) throw std::runtime_error(message); }
std::string Hex(const std::array<unsigned char,32>& hash) {
    static constexpr char alphabet[]="0123456789abcdef";
    std::string result; result.reserve(64);
    for (auto b:hash) {result+=alphabet[b>>4];result+=alphabet[b&15];} return result;
}
std::string FileHash(const std::filesystem::path& path) {
    auto name=path.u8string(); std::array<unsigned char,32> hash{};
    Need(rg_sha256_file(reinterpret_cast<const unsigned char*>(name.data()),name.size(),hash.data())==0,"cannot hash model file");
    return Hex(hash);
}
bool ValidHash(const std::string& s) {
    return s.size()==64 && std::all_of(s.begin(),s.end(),[](char c){return (c>='0'&&c<='9')||(c>='a'&&c<='f');});
}
struct TokenizerDelete { void operator()(void* p) const {rg_tokenizer_free(p);} };
struct EncodingDelete { void operator()(void* p) const {rg_encoding_free(p);} };
}

std::string HashBytes(const std::string& bytes) {
    std::array<unsigned char,32> hash{};
    Need(rg_sha256(reinterpret_cast<const unsigned char*>(bytes.data()),bytes.size(),hash.data())==0,"cannot hash bytes");
    return Hex(hash);
}
std::string ReadBounded(const std::filesystem::path& path,std::size_t maximum) {
    Need(std::filesystem::is_regular_file(path),"model artifact is not a regular file");
    const auto size=std::filesystem::file_size(path); Need(size>0 && size<=maximum,"model artifact size invalid");
    std::ifstream input(path,std::ios::binary); Need(bool(input),"cannot open model artifact");
    std::string bytes(static_cast<std::size_t>(size),'\0');
    input.read(bytes.data(),static_cast<std::streamsize>(size));
    Need(input.gcount()==static_cast<std::streamsize>(size) && input.peek()==std::char_traits<char>::eof(),"model artifact changed while reading");
    return bytes;
}

struct ModelRuntime::Impl {
    v1::ModelManifest manifest;
    std::unique_ptr<void,TokenizerDelete> tokenizer;
    Ort::Env environment{ORT_LOGGING_LEVEL_WARNING,"regulagraph"};
    Ort::Session session{nullptr};
    std::int64_t padding=0;
    std::size_t maximum_padded_tokens=0;
};

ModelRuntime::ModelRuntime(const std::filesystem::path& bundle,const std::string& pin,
                           unsigned threads,std::size_t maximum) {
    static std::once_flag initialized;
    std::call_once(initialized,[]{auto* api=OrtGetApiBase()->GetApi(ORT_API_VERSION);
        Need(api!=nullptr,"ONNX Runtime DLL does not support the compiled API version");Ort::InitApi(api);});
    impl_=std::make_unique<Impl>();
    Need(ValidHash(pin) && threads>0 && threads<=64 && maximum>0 && maximum<=131072,"invalid runtime limits/pin");
    const auto manifest_bytes=ReadBounded(bundle/"model.pbjson",65536);
    Need(HashBytes(manifest_bytes)==pin,"model manifest hash mismatch");
    Need(google::protobuf::util::JsonStringToMessage(manifest_bytes,&impl_->manifest).ok(),"invalid model manifest JSON");
    contracts::validate(impl_->manifest);
    const auto& m=impl_->manifest;
    Need(m.max_tokens()<=8192 && m.max_tokens()<=maximum,"model tokens exceed runtime capacity");
    const bool embed=m.task()==v1::MODEL_TASK_EMBED;
    Need(embed || m.task()==v1::MODEL_TASK_RERANK,"unsupported model task");
    Need(embed?(m.pooling()=="cls" && m.normalization()=="l2" && m.dimensions()<=4096):
               (m.pooling()=="logit" && m.normalization()=="none" && !m.has_dimensions()),"unsupported pooling/normalization");
    const std::string backend="onnxruntime:"+std::string(OrtGetApiBase()->GetVersionString());
    const bool cuda=m.backend()==backend+":cuda";
    Need(cuda || m.backend()==backend+":cpu","runtime backend/version mismatch");
    Need(m.precision()=="fp32" || (cuda && m.precision()=="fp16"),"unsupported model precision");
    const auto catalog_bytes=ReadBounded(bundle/"weights.json",65536);
    Need(HashBytes(catalog_bytes)==m.weights_hash().sha256(),"weights catalog hash mismatch");
    google::protobuf::Struct catalog;
    Need(google::protobuf::util::JsonStringToMessage(catalog_bytes,&catalog).ok(),"invalid weights catalog");
    Need(catalog.fields().count("model.onnx")==1 && catalog.fields_size()<=2,"invalid model file list");
    for(const auto& pair:catalog.fields()) {
        Need(pair.first=="model.onnx" || pair.first=="model.onnx.data","unexpected weights path");
        Need(pair.second.kind_case()==google::protobuf::Value::kStringValue && ValidHash(pair.second.string_value()),"invalid weights hash");
        Need(FileHash(bundle/pair.first)==pair.second.string_value(),"model weights hash mismatch");
    }
    VerifyOnnxDependencies(bundle/"model.onnx",catalog.fields().count("model.onnx.data")!=0);
    auto tokenizer_bytes=ReadBounded(bundle/"tokenizer.json",64*1024*1024);
    Need(HashBytes(tokenizer_bytes)==m.tokenizer_hash().sha256(),"tokenizer hash mismatch");
    void* tokenizer=nullptr;
    Need(rg_tokenizer_load(reinterpret_cast<const unsigned char*>(tokenizer_bytes.data()),tokenizer_bytes.size(),&tokenizer)==0,"invalid tokenizer");
    impl_->tokenizer.reset(tokenizer);
    Ort::SessionOptions options;
    options.SetIntraOpNumThreads(static_cast<int>(threads)); options.SetInterOpNumThreads(1);
    options.SetGraphOptimizationLevel(GraphOptimizationLevel::ORT_ENABLE_ALL);
    if (cuda) {
        auto providers=Ort::GetAvailableProviders();
        Need(std::find(providers.begin(),providers.end(),"CUDAExecutionProvider")!=providers.end(),"CUDA provider unavailable");
        OrtCUDAProviderOptions config{}; config.device_id=0;
        config.cudnn_conv_algo_search=OrtCudnnConvAlgoSearchHeuristic;
        config.gpu_mem_limit=std::numeric_limits<std::size_t>::max();
        config.do_copy_in_default_stream=1;
        options.AppendExecutionProvider_CUDA(config);
    }
    impl_->session=Ort::Session(impl_->environment,(bundle/"model.onnx").c_str(),options);
    Need(impl_->session.GetInputCount()==2 && impl_->session.GetOutputCount()==1,"unsupported ONNX signature");
    Ort::AllocatorWithDefaultOptions allocator;
    for(std::size_t i=0;i<2;++i) {
        auto name=impl_->session.GetInputNameAllocated(i,allocator);
        Need(std::string(name.get())==(i==0?"input_ids":"attention_mask"),"input name/order mismatch");
        auto type_info=impl_->session.GetInputTypeInfo(i);
        auto info=type_info.GetTensorTypeAndShapeInfo();
        Need(info.GetElementType()==ONNX_TENSOR_ELEMENT_DATA_TYPE_INT64 && info.GetShape().size()==2,"input type mismatch");
    }
    auto name=impl_->session.GetOutputNameAllocated(0,allocator);
    Need(std::string(name.get())==(embed?"embedding":"scores"),"output name mismatch");
    auto meta=impl_->session.GetModelMetadata();
    auto pad=meta.LookupCustomMetadataMapAllocated("regulagraph.pad_token_id",allocator);
    auto task=meta.LookupCustomMetadataMapAllocated("regulagraph.task",allocator);
    Need(pad && task && std::string(task.get())==(embed?"embed":"rerank"),"missing model metadata");
    std::size_t consumed=0; impl_->padding=std::stoll(pad.get(),&consumed);
    Need(consumed==std::string(pad.get()).size() && impl_->padding>=0 && impl_->padding<=UINT32_MAX,"invalid padding ID");
    impl_->maximum_padded_tokens=maximum;
}
ModelRuntime::~ModelRuntime()=default;
const v1::ModelManifest& ModelRuntime::Manifest() const {return impl_->manifest;}
std::size_t ModelRuntime::MaximumPaddedTokens() const {return impl_->maximum_padded_tokens;}
Tokens ModelRuntime::Encode(const std::string& text,const std::string* pair) const {
    Need(!text.empty() && text.size()<=1024*1024 && (!pair || (!pair->empty() && pair->size()<=1024*1024)),"invalid text size");
    void* encoded=nullptr;
    Need(rg_encode(impl_->tokenizer.get(),reinterpret_cast<const unsigned char*>(text.data()),text.size(),
          pair?reinterpret_cast<const unsigned char*>(pair->data()):nullptr,pair?pair->size():0,&encoded)==0,"tokenization failed");
    std::unique_ptr<void,EncodingDelete> holder(encoded);
    const auto count=rg_encoding_len(encoded);
    if(count==0 || count>impl_->manifest.max_tokens()) throw std::length_error("input exceeds pinned token limit; rechunk explicitly");
    Tokens tokens;
    tokens.ids.assign(rg_encoding_ids(encoded),rg_encoding_ids(encoded)+count);
    tokens.types.assign(rg_encoding_types(encoded),rg_encoding_types(encoded)+count);
    return tokens;
}

TensorResults ModelRuntime::Run(const std::vector<Tokens>& inputs,const std::atomic<bool>& cancel) {
    Need(!inputs.empty() && inputs.size()<=128,"invalid inference batch size");
    std::size_t length=0;
    for(const auto& item:inputs) {Need(!item.ids.empty() && item.ids.size()<=impl_->manifest.max_tokens(),"invalid token count"); length=std::max(length,item.ids.size());}
    Need(inputs.size()<=impl_->maximum_padded_tokens/length,"padded batch exceeds token budget");
    Need(!cancel.load(),"inference cancelled");
    std::vector<std::int64_t> ids(inputs.size()*length,impl_->padding),mask(ids.size(),0);
    for(std::size_t i=0;i<inputs.size();++i) {
        std::copy(inputs[i].ids.begin(),inputs[i].ids.end(),ids.begin()+i*length);
        std::fill_n(mask.begin()+i*length,inputs[i].ids.size(),1);
    }
    std::array<std::int64_t,2> shape{static_cast<std::int64_t>(inputs.size()),static_cast<std::int64_t>(length)};
    auto memory=Ort::MemoryInfo::CreateCpu(OrtArenaAllocator,OrtMemTypeDefault);
    std::array<Ort::Value,2> tensors{
        Ort::Value::CreateTensor<std::int64_t>(memory,ids.data(),ids.size(),shape.data(),2),
        Ort::Value::CreateTensor<std::int64_t>(memory,mask.data(),mask.size(),shape.data(),2)};
    const char* names[]={"input_ids","attention_mask"};
    const char* output=impl_->manifest.task()==v1::MODEL_TASK_EMBED?"embedding":"scores";
    Ort::RunOptions run;
    std::mutex mutex; std::condition_variable cv; bool finished=false;
    std::thread monitor([&]{std::unique_lock<std::mutex> lock(mutex);
        while(!cv.wait_for(lock,std::chrono::milliseconds(2),[&]{return finished;})) {
            if(cancel.load()) {try {run.SetTerminate();} catch(...) {} break;}
        }});
    auto join=[&]{ {std::lock_guard<std::mutex> lock(mutex);finished=true;} cv.notify_one();monitor.join();};
    std::vector<Ort::Value> values;
    try {values=impl_->session.Run(run,names,tensors.data(),2,&output,1);} catch(...) {join();throw;}
    join(); Need(!cancel.load(),"inference cancelled");
    const auto info=values[0].GetTensorTypeAndShapeInfo(); const auto dimensions=info.GetShape();
    const bool embed=impl_->manifest.task()==v1::MODEL_TASK_EMBED;
    const auto columns=embed?impl_->manifest.dimensions():1;
    Need(info.GetElementType()==ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT && dimensions.size()==(embed?2:1) &&
         dimensions[0]==static_cast<std::int64_t>(inputs.size()) &&
         (!embed || dimensions[1]==columns) && info.GetElementCount()==inputs.size()*columns,"model output shape/type mismatch");
    const float* data=values[0].GetTensorData<float>();
    return {std::vector<float>(data,data+inputs.size()*columns),inputs.size(),columns};
}
}
#endif

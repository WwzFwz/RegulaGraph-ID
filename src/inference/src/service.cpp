// Implements C01 embedding/reranking with one shared compute worker and bounded per-model queues.
// Tokenization happens before enqueue; internal batches preserve per-item results and explicit errors.
// A cancelled caller marks its items; ONNX is terminated only when every executing item is cancelled.
// Deadlines, padded-token capacity, queue memory and model hashes are enforced. No hidden truncation.
// Required performance/quality gates remain configs/benchmark-targets.yaml; this service records
// elapsed duration and maximum per-item queue delay, while acceptance measures client-observed time.
#include "regulagraph/inference/service.hpp"
#include "regulagraph/inference/batching.hpp"
#include "regulagraph/inference/embeddings.hpp"
#include "regulagraph/inference/cross_encoder.hpp"
#include "wire_validation.hpp"
#include <google/protobuf/util/message_differencer.h>
#include <algorithm>
#include <condition_variable>
#include <future>
#include <mutex>
#include <thread>

namespace regulagraph::inference {
namespace {
constexpr std::size_t MaxItems=128, MaxBytes=4*1024*1024, QueueBytes=64*1024*1024;
struct Result {v1::Embedding embedding;v1::RerankScore score;v1::OperationError error;std::uint64_t queue_ns=0;};
struct Work {
    std::string ticket,id; Tokens tokens; WorkClass kind; BatchTime entered,deadline;
    std::atomic<bool> cancelled{false}; std::promise<Result> promise;
    std::size_t Bytes() const {return (tokens.ids.size()+tokens.types.size())*sizeof(std::int64_t);}
};
Result Error(v1::ErrorCode code,const char* message,const std::string& id) {
    Result result;result.error.set_code(code);result.error.set_safe_message(message);
    result.error.set_stage("inference");result.error.set_item_id(id);
    result.error.set_retryable(code==v1::ERROR_CODE_UNAVAILABLE || code==v1::ERROR_CODE_RESOURCE_EXHAUSTED);
    return result;
}
bool Expired(const std::shared_ptr<Work>& w) {return w->cancelled.load() || BatchClock::now()>=w->deadline;}
std::uint64_t Ns(BatchTime start) {return std::chrono::duration_cast<std::chrono::nanoseconds>(BatchClock::now()-start).count();}
grpc::Status Context(const v1::RequestContext& request,grpc::ServerContext* context,BatchTime& deadline) {
    if(request.schema_version()!=1) return {grpc::StatusCode::INVALID_ARGUMENT,"unsupported schema"};
    const auto wall=std::chrono::system_clock::now();
    const auto seconds=std::chrono::duration_cast<std::chrono::seconds>(wall.time_since_epoch()).count();
    if(request.deadline().seconds()<seconds)
        return {grpc::StatusCode::DEADLINE_EXCEEDED,"deadline expired"};
    if(request.deadline().seconds()>seconds+1800)
        return {grpc::StatusCode::INVALID_ARGUMENT,"deadline must be within the next 30 minutes"};
    const auto wire=std::chrono::system_clock::time_point(std::chrono::duration_cast<std::chrono::system_clock::duration>(
        std::chrono::seconds(request.deadline().seconds())+std::chrono::nanoseconds(request.deadline().nanos())));
    const auto target=std::min(wire,context->deadline());
    if(target<=wall) return {grpc::StatusCode::DEADLINE_EXCEEDED,"deadline expired"};
    deadline=BatchClock::now()+std::chrono::duration_cast<BatchClock::duration>(target-wall);
    return grpc::Status::OK;
}
}

struct InferenceService::Impl {
    struct Lane {
        std::unique_ptr<ModelRuntime> model;
        BatchScheduler scheduler;
        std::unordered_map<std::string,std::shared_ptr<Work>> items;
        Lane(std::unique_ptr<ModelRuntime> value):model(std::move(value)),scheduler(SchedulerLimits{
            32,model->MaximumPaddedTokens(),128,256,std::chrono::milliseconds(2),8}){}
    };
    std::vector<std::unique_ptr<Lane>> lanes;
    std::mutex mutex; std::condition_variable cv; bool stopping=false;
    std::thread worker; std::size_t pending_bytes=0,rotation=0;std::uint64_t ticket=0;
    std::atomic<unsigned> calls{0}, bulk_calls{0};
    explicit Impl(std::vector<std::unique_ptr<ModelRuntime>> models) {
        if(models.empty() || models.size()>2) throw std::runtime_error("one or two model sessions required");
        for(auto& model:models) {
            for(const auto& lane:lanes) if(lane->model->Manifest().task()==model->Manifest().task())
                throw std::runtime_error("duplicate model task");
            // Warm the actual graph before advertising readiness.
            const std::string text="Pasal 1 ketentuan umum";
            std::atomic<bool> cancel{false};
            auto tokens=model->Encode(text,model->Manifest().task()==v1::MODEL_TASK_RERANK?&text:nullptr);
            auto output=model->Run({tokens},cancel);
            if(model->Manifest().task()==v1::MODEL_TASK_EMBED) CheckedEmbedding(output,0,model->Manifest(),tokens.ids.size());
            else CheckedScore(output,0,model->Manifest(),tokens.ids.size());
            lanes.push_back(std::make_unique<Lane>(std::move(model)));
        }
        worker=std::thread([this]{Loop();});
    }
    ~Impl() {
        {std::lock_guard<std::mutex> lock(mutex);stopping=true;
         for(auto& lane:lanes) for(auto& pair:lane->items) pair.second->cancelled.store(true);}
        cv.notify_all();worker.join();
    }
    Lane* Find(const v1::ModelManifest& manifest) {
        for(auto& lane:lanes) if(google::protobuf::util::MessageDifferencer::Equals(manifest,lane->model->Manifest())) return lane.get();
        return nullptr;
    }
    void Expire(Lane& lane,const std::vector<BatchItem>& items) {
        for(const auto& item:items) {auto pos=lane.items.find(item.item_id); if(pos==lane.items.end()) continue;
            auto work=pos->second;pending_bytes-=work->Bytes();lane.items.erase(pos);
            work->promise.set_value(Error(v1::ERROR_CODE_DEADLINE_EXCEEDED,"queued deadline expired",work->id));}
    }
    void Enqueue(Lane& lane,const std::shared_ptr<Work>& work) {
        std::lock_guard<std::mutex> lock(mutex);
        if(stopping || work->Bytes()>QueueBytes-pending_bytes) {
            work->promise.set_value(Error(v1::ERROR_CODE_RESOURCE_EXHAUSTED,"native queue full",work->id));return;}
        work->ticket=std::to_string(++ticket);
        work->entered=BatchClock::now();
        auto admission=lane.scheduler.Enqueue({work->ticket,work->kind,static_cast<std::uint32_t>(work->tokens.ids.size()),work->deadline},work->entered);
        Expire(lane,admission.expired);
        if(admission.status!=EnqueueStatus::accepted) {
            work->promise.set_value(Error(admission.status==EnqueueStatus::expired?v1::ERROR_CODE_DEADLINE_EXCEEDED:v1::ERROR_CODE_RESOURCE_EXHAUSTED,
                "native queue rejected item",work->id));return;}
        pending_bytes+=work->Bytes();lane.items.emplace(work->ticket,work);cv.notify_one();
    }
    void Loop() {
        for(;;) {
            std::vector<std::shared_ptr<Work>> batch;Lane* selected=nullptr;
            {
                std::unique_lock<std::mutex> lock(mutex);
                if(stopping) {
                    for(auto& lane:lanes) for(auto& pair:lane->items)
                        pair.second->promise.set_value(Error(v1::ERROR_CODE_CANCELLED,"runtime stopping",pair.second->id));
                    return;
                }
                auto wake=BatchClock::now()+std::chrono::milliseconds(5);
                for(std::size_t n=0;n<lanes.size();++n) {
                    const auto index=(rotation+n)%lanes.size();auto& lane=*lanes[index];
                    // Cancelled items are removed before batching; never consume query capacity until deadline.
                    for(auto it=lane.items.begin();it!=lane.items.end();) {
                        if(it->second->cancelled.load()) {auto w=it->second;lane.scheduler.Cancel(it->first);pending_bytes-=w->Bytes();
                            it=lane.items.erase(it);w->promise.set_value(Error(v1::ERROR_CODE_CANCELLED,"caller cancelled",w->id));}
                        else ++it;
                    }
                    auto picked=lane.scheduler.Next(BatchClock::now());Expire(lane,picked.expired);
                    if(!picked.items.empty()) {
                        selected=&lane;rotation=(index+1)%lanes.size();
                        for(const auto& item:picked.items) {auto w=lane.items.at(item.item_id);pending_bytes-=w->Bytes();
                            lane.items.erase(item.item_id);batch.push_back(std::move(w));} break;
                    }
                    const auto next=lane.scheduler.NextWakeup();if(next) wake=std::min(wake,*next);
                }
                if(!selected) {cv.wait_until(lock,wake);continue;}
            }
            // Sort by actual sequence length and cap padded, not merely summed, token memory.
            std::stable_sort(batch.begin(),batch.end(),[](const auto& a,const auto& b){return a->tokens.ids.size()<b->tokens.ids.size();});
            std::size_t first=0;
            while(first<batch.size()) {
                std::size_t end=first+1;
                while(end<batch.size() && (end-first+1)<=selected->model->MaximumPaddedTokens()/batch[end]->tokens.ids.size()) ++end;
                Execute(*selected,batch,first,end); first=end;
            }
        }
    }
    void Execute(Lane& lane,const std::vector<std::shared_ptr<Work>>& chosen,std::size_t first,std::size_t end) {
        std::vector<std::shared_ptr<Work>> active;std::vector<Tokens> inputs;
        for(auto i=first;i<end;++i) {
            auto w=chosen[i];
            if(Expired(w)) w->promise.set_value(Error(w->cancelled?v1::ERROR_CODE_CANCELLED:v1::ERROR_CODE_DEADLINE_EXCEEDED,"item expired before inference",w->id));
            else {active.push_back(w);inputs.push_back(w->tokens);}
        }
        if(active.empty()) return;
        const auto started=BatchClock::now();std::atomic<bool> cancel{false};
        std::mutex done_mutex;std::condition_variable done_cv;bool done=false;
        std::thread monitor([&]{std::unique_lock<std::mutex> lock(done_mutex);
            while(!done_cv.wait_for(lock,std::chrono::milliseconds(2),[&]{return done;})) {
                bool stop=false;{std::lock_guard<std::mutex> state(mutex);stop=stopping;}
                if(stop || std::all_of(active.begin(),active.end(),Expired)) {cancel.store(true);break;}
            }});
        std::vector<Result> results(active.size());
        try {
            auto tensor=lane.model->Run(inputs,cancel);
            for(std::size_t i=0;i<active.size();++i) {
                if(Expired(active[i])) results[i]=Error(active[i]->cancelled?v1::ERROR_CODE_CANCELLED:v1::ERROR_CODE_DEADLINE_EXCEEDED,"item expired during inference",active[i]->id);
                else if(lane.model->Manifest().task()==v1::MODEL_TASK_EMBED) results[i].embedding=CheckedEmbedding(tensor,i,lane.model->Manifest(),inputs[i].ids.size());
                else results[i].score=CheckedScore(tensor,i,lane.model->Manifest(),inputs[i].ids.size());
            }
        } catch(const std::exception&) {
            for(std::size_t i=0;i<active.size();++i) results[i]=Error(cancel?v1::ERROR_CODE_CANCELLED:v1::ERROR_CODE_INTERNAL,"native model execution failed",active[i]->id);
        }
        {std::lock_guard<std::mutex> lock(done_mutex);done=true;}done_cv.notify_one();monitor.join();
        for(std::size_t i=0;i<active.size();++i) {
            results[i].queue_ns=std::chrono::duration_cast<std::chrono::nanoseconds>(started-active[i]->entered).count();
            active[i]->promise.set_value(std::move(results[i]));
        }
    }
};

InferenceService::InferenceService(std::vector<std::unique_ptr<ModelRuntime>> models):impl_(std::make_unique<Impl>(std::move(models))){}
InferenceService::~InferenceService()=default;

// Both operations use the same bounded pipeline; protobuf result projection remains task-specific.
template<class Request,class Response,class Impl,class Texts,class Project>
grpc::Status Process(Impl& impl,grpc::ServerContext* ctx,const Request& request,Response& response,
                     Texts texts,Project project,WorkClass kind) {
    const auto started=BatchClock::now();
    // Keep at least six of eight RPC slots available for query/rerank traffic.
    const bool bulk=kind==WorkClass::bulk;
    if(bulk && impl.bulk_calls.fetch_add(1)>=2) {
        impl.bulk_calls.fetch_sub(1);return {grpc::StatusCode::RESOURCE_EXHAUSTED,"bulk admission full"};
    }
    struct BulkGuard {std::atomic<unsigned>& calls;bool active;~BulkGuard(){if(active)calls.fetch_sub(1);}} bulk_guard{impl.bulk_calls,bulk};
    if(impl.calls.fetch_add(1)>=8) {impl.calls.fetch_sub(1);return {grpc::StatusCode::RESOURCE_EXHAUSTED,"too many active native requests"};}
    struct Guard {std::atomic<unsigned>& calls;~Guard(){calls.fetch_sub(1);}} guard{impl.calls};
    try {contracts::validate(request,{MaxBytes,64,100000});} catch(const std::exception&) {
        return {grpc::StatusCode::INVALID_ARGUMENT,"invalid inference request"};}
    BatchTime deadline;auto status=Context(request.context(),ctx,deadline);if(!status.ok()) return status;
    auto* lane=impl.Find(request.model());if(!lane) return {grpc::StatusCode::FAILED_PRECONDITION,"requested model is not loaded"};
    auto rows=texts();if(rows.empty() || rows.size()>MaxItems) return {grpc::StatusCode::RESOURCE_EXHAUSTED,"request item limit exceeded"};
    std::vector<std::shared_ptr<Work>> works;std::vector<std::future<Result>> futures;
    for(const auto& row:rows) {
        auto work=std::make_shared<Work>();work->id=std::get<0>(row);work->entered=BatchClock::now();work->deadline=deadline;work->kind=kind;
        futures.push_back(work->promise.get_future());works.push_back(work);
        if(ctx->IsCancelled() || BatchClock::now()>=deadline) {
            work->promise.set_value(Error(v1::ERROR_CODE_CANCELLED,"caller cancelled during tokenization",work->id));continue;}
        try {work->tokens=lane->model->Encode(*std::get<1>(row),std::get<2>(row));}
        catch(const std::length_error&) {work->promise.set_value(Error(v1::ERROR_CODE_TOO_LARGE,"input exceeds pinned token limit; rechunk explicitly",work->id));continue;}
        catch(const std::exception&) {work->promise.set_value(Error(v1::ERROR_CODE_INVALID_ARGUMENT,"text cannot be tokenized",work->id));continue;}
        impl.Enqueue(*lane,work);
    }
    std::uint64_t queue=0;
    response.set_request_id(request.context().request_id());*response.mutable_model()=lane->model->Manifest();
    for(std::size_t i=0;i<futures.size();++i) {
        while(futures[i].wait_for(std::chrono::milliseconds(2))!=std::future_status::ready) {
            if(ctx->IsCancelled() || BatchClock::now()>=deadline) {
                for(auto& w:works) w->cancelled.store(true);impl.cv.notify_one();
                return {ctx->IsCancelled()?grpc::StatusCode::CANCELLED:grpc::StatusCode::DEADLINE_EXCEEDED,"inference request cancelled or expired"};
            }
        }
        auto result=futures[i].get();queue=std::max(queue,result.queue_ns);project(response,works[i]->id,result);
    }
    auto* duration=response.add_durations();duration->set_stage("inference.total");duration->set_duration_ns(Ns(started));duration->set_queue_ns(queue);
    return grpc::Status::OK;
}
using TextRows=std::vector<std::tuple<std::string,const std::string*,const std::string*>>;
grpc::Status InferenceService::EmbedBatch(grpc::ServerContext* ctx,const v1::EmbedBatchRequest* req,v1::EmbedBatchResponse* out) {
    return Process(*impl_,ctx,*req,*out,[&]{TextRows rows;for(const auto& r:req->items()) rows.emplace_back(r.item_id(),&r.text(),nullptr);return rows;},
        [](auto& response,const auto& id,const auto& r){auto* item=response.add_results();item->set_item_id(id);
            if(r.error.code()!=v1::ERROR_CODE_UNSPECIFIED) *item->mutable_error()=r.error;else *item->mutable_embedding()=r.embedding;},
        req->purpose()==v1::EMBEDDING_PURPOSE_QUERY?WorkClass::query:WorkClass::bulk);
}
grpc::Status InferenceService::RerankBatch(grpc::ServerContext* ctx,const v1::RerankBatchRequest* req,v1::RerankBatchResponse* out) {
    return Process(*impl_,ctx,*req,*out,[&]{TextRows rows;for(const auto& r:req->pairs()) rows.emplace_back(r.pair_id(),&r.query(),&r.text());return rows;},
        [](auto& response,const auto& id,const auto& r){auto* item=response.add_results();item->set_pair_id(id);
            if(r.error.code()!=v1::ERROR_CODE_UNSPECIFIED) *item->mutable_error()=r.error;else *item->mutable_score()=r.score;},WorkClass::query);
}
grpc::Status InferenceService::GetCapabilities(grpc::ServerContext* ctx,const v1::CapabilitiesRequest* req,v1::CapabilitiesResponse* out) {
    try {contracts::validate(*req);} catch(...) {return {grpc::StatusCode::INVALID_ARGUMENT,"invalid capability context"};}
    BatchTime deadline;auto status=Context(req->context(),ctx,deadline);if(!status.ok()) return status;
    out->set_ready(true);
    for(const auto& lane:impl_->lanes) {auto* cap=out->add_models();*cap->mutable_model()=lane->model->Manifest();cap->set_ready(true);
        cap->mutable_limits()->set_max_items(MaxItems);cap->mutable_limits()->set_max_bytes(MaxBytes);
        cap->mutable_limits()->set_max_tokens(lane->model->Manifest().max_tokens());out->add_supported_tasks(lane->model->Manifest().task());}
    return grpc::Status::OK;
}
}

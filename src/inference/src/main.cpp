// Native inference executable: validates pinned operator bundles, warms sessions, then serves C01 gRPC.
// Loopback-only transport matches the current internal worker boundary; deployment TLS is separate.
// Startup failures never report ready. Signals stop admission and bound RPC shutdown before session teardown.
// Cold startup is separate from queue-inclusive warm MODEL benchmarks in configs/benchmark-targets.yaml.
#include "regulagraph/inference/service.hpp"
#include <grpcpp/grpcpp.h>
#include <atomic>
#include <csignal>
#include <iostream>
#include <map>
#include <thread>

namespace {std::atomic<bool> stopping{false};void Stop(int){stopping.store(true);}}
int main(int argc,char** argv) {
    try {
        std::map<std::string,std::string> options;
        for(int i=1;i<argc;i+=2) {
            if(i+1>=argc || !options.emplace(argv[i],argv[i+1]).second) throw std::runtime_error("expected unique --option value arguments");
        }
        for(const auto& pair:options) if(pair.first!="--bundle" && pair.first!="--manifest-sha256" &&
            pair.first!="--reranker" && pair.first!="--reranker-sha256" && pair.first!="--listen" &&
            pair.first!="--threads" && pair.first!="--batch-tokens") throw std::runtime_error("unknown option");
        auto address=options.count("--listen")?options.at("--listen"):"127.0.0.1:50054";
        std::string port;
        if(address.rfind("127.0.0.1:",0)==0) port=address.substr(10);
        else if(address.rfind("[::1]:",0)==0) port=address.substr(6);
        else throw std::runtime_error("native inference listener must be loopback");
        std::size_t consumed=0;auto number=std::stoul(port,&consumed);
        if(consumed!=port.size() || number==0 || number>65535) throw std::runtime_error("invalid listener port");
        auto parse=[&](const char* key,unsigned fallback) {
            if(!options.count(key)) return fallback;std::size_t n=0;auto value=std::stoul(options.at(key),&n);
            if(n!=options.at(key).size() || value>131072) throw std::runtime_error("invalid numeric option");return static_cast<unsigned>(value);
        };
        const auto threads=parse("--threads",4), tokens=parse("--batch-tokens",16384);
        std::vector<std::unique_ptr<regulagraph::inference::ModelRuntime>> models;
        if(options.count("--bundle")) models.push_back(std::make_unique<regulagraph::inference::ModelRuntime>(
            std::filesystem::u8path(options.at("--bundle")),options.at("--manifest-sha256"),threads,tokens));
        if(options.count("--reranker")) models.push_back(std::make_unique<regulagraph::inference::ModelRuntime>(
            std::filesystem::u8path(options.at("--reranker")),options.at("--reranker-sha256"),threads,tokens));
        regulagraph::inference::InferenceService service(std::move(models));
        grpc::ServerBuilder builder;grpc::ResourceQuota quota;quota.SetMaxThreads(16);builder.SetResourceQuota(quota);
        builder.SetMaxReceiveMessageSize(4*1024*1024);builder.SetMaxSendMessageSize(4*1024*1024);
        builder.SetSyncServerOption(grpc::ServerBuilder::SyncServerOption::MIN_POLLERS,2);
        builder.SetSyncServerOption(grpc::ServerBuilder::SyncServerOption::MAX_POLLERS,8);
        int bound=0;builder.AddListeningPort(address,grpc::InsecureServerCredentials(),&bound);builder.RegisterService(&service);
        auto server=builder.BuildAndStart();if(!server || bound==0) throw std::runtime_error("cannot bind inference listener");
        std::signal(SIGINT,Stop);std::signal(SIGTERM,Stop);
        std::cout<<"native inference ready "<<address<<std::endl;
        while(!stopping.load()) std::this_thread::sleep_for(std::chrono::milliseconds(100));
        server->Shutdown(std::chrono::system_clock::now()+std::chrono::seconds(5));server->Wait();
        return 0;
    } catch(const std::exception& error) {std::cerr<<"native inference startup/runtime error: "<<error.what()<<std::endl;return 1;}
}

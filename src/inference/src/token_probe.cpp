// Offline token parity probe using the exact ModelRuntime::Encode used by serving.
// Reads bounded diagnostic cases and writes token IDs; it does not create a second tokenizer policy.
// Output is model-compatibility evidence only, not retrieval quality or a performance benchmark.
#include "regulagraph/inference/runtime.hpp"
#include <google/protobuf/struct.pb.h>
#include <google/protobuf/util/json_util.h>
#include <fstream>
#include <iostream>
int main(int argc,char** argv) {
    try {
        if(argc!=5) throw std::runtime_error("usage: token_probe bundle manifest_sha256 cases.json output.json");
        regulagraph::inference::ModelRuntime runtime(std::filesystem::u8path(argv[1]),argv[2]);
        google::protobuf::ListValue cases,output;
        if(!google::protobuf::util::JsonStringToMessage(regulagraph::inference::ReadBounded(std::filesystem::u8path(argv[3]),4*1024*1024),&cases).ok() ||
            cases.values_size()==0 || cases.values_size()>10000) throw std::runtime_error("invalid probe cases");
        for(const auto& value:cases.values()) {
            const auto& fields=value.struct_value().fields();
            auto text=fields.at("text").string_value();auto id=fields.at("id").string_value();
            const bool pair=runtime.Manifest().task()==regulagraph::v1::MODEL_TASK_RERANK;
            auto query=pair?fields.at("query").string_value():text;
            auto tokens=runtime.Encode(query,pair?&text:nullptr);
            auto* record=output.add_values()->mutable_struct_value();
            (*record->mutable_fields())["id"].set_string_value(id);
            auto* ids=(*record->mutable_fields())["input_ids"].mutable_list_value();
            for(auto token:tokens.ids) ids->add_values()->set_number_value(static_cast<double>(token));
        }
        std::string json;if(!google::protobuf::util::MessageToJsonString(output,&json).ok()) throw std::runtime_error("cannot serialize token probe");
        std::ofstream file(std::filesystem::u8path(argv[4]),std::ios::binary);file<<json;
        if(!file) throw std::runtime_error("cannot write token probe");return 0;
    }catch(const std::exception& error){std::cerr<<error.what()<<std::endl;return 1;}
}

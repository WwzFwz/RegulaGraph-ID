// Checks ONNX 1.18 Model/Graph/Node/Attribute/Tensor external data with bounded streaming.
// Field numbers follow upstream onnx/onnx.proto (v1.18.0); this is a dependency scanner,
// not a replacement for ONNX/ORT graph validation. Raw tensor bytes are skipped, never copied.
// Only model.onnx.data with explicit in-file offset/length is accepted; the runtime hashes it.
// Imported operator functions/training/sparse tensor graphs are outside the pinned exporter profile.
#include "regulagraph/inference/model_integrity.hpp"
#include <google/protobuf/io/coded_stream.h>
#include <google/protobuf/io/zero_copy_stream_impl.h>
#include <google/protobuf/wire_format_lite.h>
#include <fstream>
#include <climits>
#include <cstdint>
#include <map>
#include <limits>
#include <stdexcept>

namespace regulagraph::inference {
namespace {
using Input=google::protobuf::io::CodedInputStream;
enum class Kind {model,graph,node,attribute,tensor,entry};
void Require(bool yes) {if(!yes) throw std::runtime_error("unsupported or unpinned ONNX dependency");}
std::uint64_t Number(const std::string& value) {
    Require(!value.empty());std::uint64_t result=0;
    for(char c:value) {Require(c>='0'&&c<='9');Require(result<=(UINT64_MAX-(c-'0'))/10);result=result*10+(c-'0');}
    return result;
}
struct Scanner {
    bool has_sidecar,used_sidecar=false;std::uint64_t sidecar_size;std::size_t fields=0;
    std::string String(Input& in) {
        std::uint32_t length;Require(in.ReadVarint32(&length)&&length<=4096);
        std::string result;Require(in.ReadString(&result,length));return result;
    }
    void Child(Input& in,Kind kind,unsigned depth,std::map<std::string,std::string>* entries=nullptr) {
        std::uint32_t length;Require(in.ReadVarint32(&length)&&length<=INT_MAX);
        const auto limit=in.PushLimit(static_cast<int>(length));Scan(in,kind,depth+1,entries);
        Require(in.BytesUntilLimit()==0);in.PopLimit(limit);
    }
    void Scan(Input& in,Kind kind,unsigned depth,std::map<std::string,std::string>* entries=nullptr) {
        Require(depth<=64);std::map<std::string,std::string> external;
        std::string key,value;bool has_key=false,has_value=false;std::uint64_t location=0;
        while(auto tag=in.ReadTag()) {
            Require(++fields<=1000000);const auto field=tag>>3,wire=tag&7;
            Require(wire==0||wire==1||wire==2||wire==5);
            if(kind==Kind::model && (field==20||field==25)) Require(false);
            if((kind==Kind::graph&&field==15)||(kind==Kind::attribute&&(field==22||field==23))) Require(false);
            Kind child=Kind::model;bool descend=true;
            if(kind==Kind::model&&field==7) child=Kind::graph;
            else if(kind==Kind::graph&&field==1) child=Kind::node;
            else if(kind==Kind::graph&&field==5) child=Kind::tensor;
            else if(kind==Kind::node&&field==5) child=Kind::attribute;
            else if(kind==Kind::attribute&&(field==5||field==10)) child=Kind::tensor;
            else if(kind==Kind::attribute&&(field==6||field==11)) child=Kind::graph;
            else descend=false;
            if(descend) {Require(wire==2);Child(in,child,depth);}
            else if(kind==Kind::tensor&&field==13) {Require(wire==2);Child(in,Kind::entry,depth,&external);}
            else if(kind==Kind::tensor&&field==14) {Require(wire==0);Require(in.ReadVarint64(&location)&&location<=1);}
            else if(kind==Kind::entry&&(field==1||field==2)) {
                Require(wire==2);
                if(field==1) {Require(!has_key);key=String(in);has_key=true;}
                else {Require(!has_value);value=String(in);has_value=true;}
            } else Require(google::protobuf::internal::WireFormatLite::SkipField(&in,tag));
        }
        Require(in.ConsumedEntireMessage());
        if(kind==Kind::entry) {Require(entries&&has_key&&has_value&&entries->emplace(key,value).second);}
        if(kind==Kind::tensor) {
            Require(location==1 || external.empty());
            if(location==1) {
                Require(has_sidecar && external.size()==3 && external.count("location") && external.count("offset") && external.count("length"));
                Require(external.at("location")=="model.onnx.data");
                auto offset=Number(external.at("offset")),length=Number(external.at("length"));
                Require(offset<=sidecar_size && length<=sidecar_size-offset);used_sidecar=true;
            }
        }
    }
};
}
void VerifyOnnxDependencies(const std::filesystem::path& graph,bool has_sidecar) {
    const auto size=std::filesystem::file_size(graph);Require(size>0&&size<=INT_MAX);
    std::ifstream file(graph,std::ios::binary);Require(bool(file));
    google::protobuf::io::IstreamInputStream stream(&file,65536);Input input(&stream);
    input.SetTotalBytesLimit(INT_MAX);
    Scanner scanner{has_sidecar,false,has_sidecar?std::filesystem::file_size(graph.parent_path()/"model.onnx.data"):0};
    scanner.Scan(input,Kind::model,0);
    Require(static_cast<std::uintmax_t>(input.CurrentPosition())==size && scanner.used_sidecar==has_sidecar);
}
}

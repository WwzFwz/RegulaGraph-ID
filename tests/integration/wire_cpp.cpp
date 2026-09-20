// Cross-language fixture probe uses real generated C++ messages and boundary validation.
// Reads synthetic fixtures only, writes reserialized bytes, and fails on unexpected validation results.
// This proves wire compatibility/presence, not model accuracy or RPC/service availability.
#include "wire_validation.hpp"
#include "regulagraph/v1/common.pb.h"
#include "regulagraph/v1/documents.pb.h"
#include "regulagraph/v1/graph.pb.h"
#include "regulagraph/v1/evidence.pb.h"
#include "regulagraph/v1/answers.pb.h"
#include "regulagraph/v1/jobs.pb.h"
#include "regulagraph/v1/inference.pb.h"
#include "evaluation.pb.h"
#include <filesystem>
#include <fstream>
#include <iostream>
#include <sstream>
#include <memory>
int main(int argc,char**argv){
 try{
  if(argc!=2)throw std::invalid_argument("fixture directory required");
  // Force registration of every generated translation unit when linking a static library.
  (void)regulagraph::v1::CalendarDate::descriptor();(void)regulagraph::v1::DocumentBatch::descriptor();
  (void)regulagraph::v1::GraphDelta::descriptor();(void)regulagraph::v1::EvidenceBundle::descriptor();
  (void)regulagraph::v1::Answer::descriptor();(void)regulagraph::v1::Job::descriptor();
  (void)regulagraph::v1::EmbedBatchRequest::descriptor();(void)regulagraph::v1::GoldQuestion::descriptor();
  std::filesystem::path dir(argv[1]);std::filesystem::create_directories(dir/"cpp");
  std::ifstream cases(dir/"cases.tsv");if(!cases)throw std::runtime_error("missing cases");std::string line;
  while(std::getline(cases,line)){std::istringstream row(line);std::string name,type,expected,limit;std::getline(row,name,'\t');std::getline(row,type,'\t');std::getline(row,expected,'\t');std::getline(row,limit,'\t');
   auto*d=google::protobuf::DescriptorPool::generated_pool()->FindMessageTypeByName(type);if(!d)throw std::runtime_error("unknown type "+type);
   auto*p=google::protobuf::MessageFactory::generated_factory()->GetPrototype(d);std::unique_ptr<google::protobuf::Message> m(p->New());
   std::ifstream input(dir/(name+".bin"),std::ios::binary);std::string raw((std::istreambuf_iterator<char>(input)),{});
   bool ok=true;try{regulagraph::contracts::Limits l;l.max_bytes=std::stoull(limit);regulagraph::contracts::decode(raw,*m,l);}catch(const std::exception&){ok=false;}
   if(ok!=(expected=="valid"))throw std::runtime_error("validation mismatch: "+name);
   if(!m->ParseFromString(raw))throw std::runtime_error("parse failure: "+name);
   std::ofstream out(dir/"cpp"/(name+".bin"),std::ios::binary);if(!m->SerializeToOstream(&out))throw std::runtime_error("write failure");
  }
  regulagraph::contracts::check_utf8_span("Pasal \xc3\xa9",0,8);
  bool rejected=false;try{regulagraph::contracts::check_utf8_span("Pasal \xc3\xa9",0,7);}catch(const std::exception&){rejected=true;}if(!rejected)throw std::runtime_error("UTF-8 split accepted");
  std::cout<<"C++ wire fixtures passed\n";return 0;
 }catch(const std::exception&e){std::cerr<<e.what()<<"\n";return 1;}
}

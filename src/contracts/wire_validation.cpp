// Native C01 validator executes rules from proto field descriptors and local boundary invariants.
// All scalar validation is generated-schema-driven; input remains untrusted even after protobuf decoding.
// Checks are bounded by bytes/depth/items; storage referential integrity is a coordinator responsibility.
#include "wire_validation.hpp"
#include "regulagraph/v1/common.pb.h"
#include "regulagraph/v1/evidence.pb.h"
#include "regulagraph/v1/inference.pb.h"
#include <google/protobuf/io/coded_stream.h>
#include <google/protobuf/util/time_util.h>
#include <cmath>
#include <limits>
#include <sstream>
#include <stdexcept>
#include <unordered_set>
namespace regulagraph::contracts {
namespace {
using google::protobuf::FieldDescriptor;
using google::protobuf::Message;
void need(bool ok,const std::string& field){if(!ok)throw std::invalid_argument(field);}
bool valid_utf8(const std::string& s) {
 for(std::size_t i=0;i<s.size();){auto c=static_cast<unsigned char>(s[i++]);if(c<0x80)continue;
  unsigned n=0,cp=0,min=0;if(c>=0xc2&&c<=0xdf){n=1;cp=c&31;min=0x80;}else if(c>=0xe0&&c<=0xef){n=2;cp=c&15;min=0x800;}else if(c>=0xf0&&c<=0xf4){n=3;cp=c&7;min=0x10000;}else return false;
  if(i+n>s.size())return false;while(n--){auto d=static_cast<unsigned char>(s[i++]);if((d&0xc0)!=0x80)return false;cp=(cp<<6)|(d&63);}if(cp<min||cp>0x10ffff||(cp>=0xd800&&cp<=0xdfff))return false;
 }return true;
}
void semantic(const Message& m){
 if(auto p=dynamic_cast<const v1::ArtifactRef*>(&m)){const auto&k=p->storage_key();need(k.find(':')==std::string::npos&&k.find('\\')==std::string::npos&&!k.empty()&&k.back()!='/',"ArtifactRef storage key");std::istringstream in(k);std::string part;while(std::getline(in,part,'/'))need(!part.empty()&&part!="."&&part!="..","ArtifactRef storage key");}
 if(auto p=dynamic_cast<const v1::TemporalScope*>(&m)){need(p->mode()!=v1::TEMPORAL_MODE_AS_OF||p->has_effective_at(),"TemporalScope AS_OF");need(p->mode()==v1::TEMPORAL_MODE_COMPARE ? p->compare_dates_size()>=2&&!p->has_effective_at() : p->compare_dates_size()==0,"TemporalScope COMPARE");}
 if(auto p=dynamic_cast<const v1::GraphPath*>(&m))need(p->ordered_node_ids_size()==p->ordered_assertion_ids_size()+1&&p->selected_support_ids_size()==p->ordered_assertion_ids_size(),"GraphPath cardinality");
 if(auto p=dynamic_cast<const v1::SourceBlob*>(&m))need(p->byte_size()==p->artifact_ref().byte_size()&&p->raw_sha256().sha256()==p->artifact_ref().content_hash().sha256(),"SourceBlob artifact");
 if(auto p=dynamic_cast<const v1::SourceObservation*>(&m))need(p->status()!=v1::OBSERVATION_STATUS_COMPLETE||(p->has_source_blob_id()&&!p->has_error()),"SourceObservation completion");

 if(auto p=dynamic_cast<const v1::CalendarDate*>(&m)){int y=p->year();auto month=p->month(),day=p->day();need(y>0&&y<=9999&&month>0&&month<=12,"CalendarDate");unsigned days[]={31,28,31,30,31,30,31,31,30,31,30,31};if(y%4==0&&(y%100!=0||y%400==0))days[1]=29;need(day>0&&day<=days[month-1],"CalendarDate");}
 if(auto p=dynamic_cast<const v1::DateAssertion*>(&m)){need((p->knowledge()==v1::DATE_KNOWLEDGE_KNOWN)==p->has_value(),"DateAssertion value");need(p->knowledge()==v1::DATE_KNOWLEDGE_CONFLICT ? p->alternatives_size()>=2 : p->alternatives_size()==0,"DateAssertion alternatives");}
 if(auto p=dynamic_cast<const v1::Visibility*>(&m))need(!p->has_to_seq()||p->to_seq()>p->from_seq(),"Visibility");
 if(auto p=dynamic_cast<const v1::TextSpan*>(&m))need(p->end_byte()>=p->start_byte(),"TextSpan");
 if(auto p=dynamic_cast<const v1::BoundingBox*>(&m))need(p->x1()>=p->x0()&&p->y1()>=p->y0(),"BoundingBox");
 if(auto p=dynamic_cast<const v1::LegalInterval*>(&m)){if(p->start().has_value()&&p->end().has_value()){auto number=[](const v1::CalendarDate&d){return d.year()*10000+d.month()*100+d.day();};need(number(p->start().value())<number(p->end().value()),"LegalInterval");}}
 if(auto p=dynamic_cast<const v1::SparseVector*>(&m)){need(p->indices_size()==p->values_size(),"SparseVector length");for(int i=1;i<p->indices_size();++i)need(p->indices(i)>p->indices(i-1),"SparseVector order");}
 if(auto p=dynamic_cast<const v1::DenseVector*>(&m))need(p->dimensions()==static_cast<unsigned>(p->values_size()),"DenseVector dimensions");
 if(auto p=dynamic_cast<const v1::Counts*>(&m))need(p->accepted()<=p->expected()&&p->rejected()==p->expected()-p->accepted(),"Counts");
 if(auto p=dynamic_cast<const v1::TruncationInfo*>(&m))need(p->retained_tokens()<=p->original_tokens()&&(p->truncated()||p->retained_tokens()==p->original_tokens()),"TruncationInfo");
 if(auto p=dynamic_cast<const v1::RequestContext*>(&m))need(!p->has_snapshot_ref()||p->corpus_id()==p->snapshot_ref().corpus_id(),"RequestContext corpus");
 if(auto p=dynamic_cast<const google::protobuf::Timestamp*>(&m))need(google::protobuf::util::TimeUtil::IsTimestampValid(*p),"Timestamp");
 if(auto p=dynamic_cast<const v1::ModelManifest*>(&m))need(p->task()!=v1::MODEL_TASK_EMBED||p->has_dimensions(),"ModelManifest dimensions");
 if(auto p=dynamic_cast<const v1::EmbedBatchRequest*>(&m)){need(p->model().task()==v1::MODEL_TASK_EMBED,"EmbedBatch model");std::unordered_set<std::string> ids;for(auto& item:p->items())need(ids.insert(item.item_id()).second,"EmbedBatch duplicate ID");}
 if(auto p=dynamic_cast<const v1::RerankBatchRequest*>(&m)){need(p->model().task()==v1::MODEL_TASK_RERANK,"RerankBatch model");std::unordered_set<std::string> ids;for(auto& item:p->pairs())need(ids.insert(item.pair_id()).second,"RerankBatch duplicate ID");}
}
std::string corpus(const Message&m){auto*d=m.GetDescriptor();auto*r=m.GetReflection();if(auto*f=d->FindFieldByName("corpus_id"))return r->GetString(m,f);for(auto name:{"meta","context","batch"})if(auto*f=d->FindFieldByName(name))if(f->cpp_type()==FieldDescriptor::CPPTYPE_MESSAGE&&r->HasField(m,f)){auto c=corpus(r->GetMessage(m,f));if(!c.empty())return c;}return {};}
void walk(const Message&m,int depth,const Limits&lim,std::size_t&remaining,const std::string&inherited){
 auto own=corpus(m);need(own.empty()||inherited.empty()||own==inherited,"nested corpus mismatch");auto active=own.empty()?inherited:own;

 need(depth<=lim.max_depth,"depth limit");auto*d=m.GetDescriptor();auto*r=m.GetReflection();
 for(int i=0;i<d->field_count();++i){auto*f=d->field(i);const auto&rule=f->options().GetExtension(v1::rules);std::string where(f->full_name());
  int count=f->is_repeated()?r->FieldSize(m,f):1;bool present=f->is_repeated()?count>0:r->HasField(m,f);need(!rule.required()||present,where+": required");
  if(f->is_repeated()){need(count>=static_cast<int>(rule.min_items()),where+": min_items");need(static_cast<std::size_t>(count)<=remaining,"item limit");remaining-=count;}
  else if(f->has_presence()&&!present)continue;
  std::unordered_set<std::string> seen;
  for(int j=0;j<count;++j){bool rep=f->is_repeated();std::string key;double n=0;bool numeric=false;
   switch(f->cpp_type()){
   case FieldDescriptor::CPPTYPE_MESSAGE:{need(remaining>0,"item limit");--remaining;walk(rep?r->GetRepeatedMessage(m,f,j):r->GetMessage(m,f),depth+1,lim,remaining,active);continue;}
   case FieldDescriptor::CPPTYPE_STRING:{auto s=rep?r->GetRepeatedString(m,f,j):r->GetString(m,f);key=s;if(f->type()==FieldDescriptor::TYPE_STRING)need(valid_utf8(s),where+": UTF-8");if(rule.ascii_id()){need(!s.empty()&&s.size()<=256,where+": ID");for(unsigned char c:s)need(c>=33&&c<=126,where+": ID");}if(rule.sha256()){need(s.size()==64,where+": hash");for(char c:s)need((c>='0'&&c<='9')||(c>='a'&&c<='f'),where+": hash");}break;}
   case FieldDescriptor::CPPTYPE_ENUM:{int e=rep?r->GetRepeatedEnumValue(m,f,j):r->GetEnumValue(m,f);need(!rule.required()||(e!=0&&f->enum_type()->FindValueByNumber(e)),where+": enum");key=std::to_string(e);break;}
   case FieldDescriptor::CPPTYPE_INT32:n=rep?r->GetRepeatedInt32(m,f,j):r->GetInt32(m,f);numeric=true;break;
   case FieldDescriptor::CPPTYPE_INT64:n=static_cast<double>(rep?r->GetRepeatedInt64(m,f,j):r->GetInt64(m,f));numeric=true;break;
   case FieldDescriptor::CPPTYPE_UINT32:n=rep?r->GetRepeatedUInt32(m,f,j):r->GetUInt32(m,f);numeric=true;break;
   case FieldDescriptor::CPPTYPE_UINT64:n=static_cast<double>(rep?r->GetRepeatedUInt64(m,f,j):r->GetUInt64(m,f));numeric=true;break;
   case FieldDescriptor::CPPTYPE_FLOAT:n=rep?r->GetRepeatedFloat(m,f,j):r->GetFloat(m,f);numeric=true;break;
   case FieldDescriptor::CPPTYPE_DOUBLE:n=rep?r->GetRepeatedDouble(m,f,j):r->GetDouble(m,f);numeric=true;break;
   default:break;
   }
   if(numeric){key=std::to_string(n);if(rule.positive()||rule.finite()||rule.probability())need(std::isfinite(n)&&(!rule.positive()||n>0)&&(!rule.probability()||(n>=0&&n<=1)),where+": numeric");if(f->name()=="schema_version")need(n==1,where+": version");}
   if(rule.unique())need(seen.insert(key).second,where+": duplicate");
  }
 }
 for(int i=0;i<d->real_oneof_decl_count();++i){auto*o=d->oneof_decl(i);need(r->HasOneof(m,o),std::string(o->full_name())+": required");}
 semantic(m);
}
}
void validate(const google::protobuf::Message&m,Limits limits){need(limits.max_bytes>0&&limits.max_depth>0&&limits.max_items>0,"invalid limits");need(m.ByteSizeLong()<=limits.max_bytes,"byte limit");auto remaining=limits.max_items;walk(m,0,limits,remaining,"");}
void decode(const std::string&raw,google::protobuf::Message&m,Limits limits){need(limits.max_bytes>0&&limits.max_depth>0&&limits.max_items>0,"invalid limits");need(raw.size()<=limits.max_bytes&&raw.size()<=static_cast<std::size_t>(std::numeric_limits<int>::max()),"byte limit");google::protobuf::io::CodedInputStream input(reinterpret_cast<const std::uint8_t*>(raw.data()),static_cast<int>(raw.size()));input.SetRecursionLimit(limits.max_depth);need(m.ParseFromCodedStream(&input)&&input.ConsumedEntireMessage(),"invalid wire");validate(m,limits);}
void check_utf8_span(const std::string&s,std::size_t start,std::size_t end){need(valid_utf8(s)&&start<=end&&end<=s.size(),"UTF-8/span bounds");auto boundary=[&](std::size_t i){return i==s.size()||(static_cast<unsigned char>(s[i])&0xc0)!=0x80;};need(boundary(start)&&boundary(end),"UTF-8 split");}
}

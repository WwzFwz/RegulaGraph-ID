"""
Membandingkan output model referensi dengan runtime native.

Peran dalam komponen:
Menilai kesetaraan embedding/skor sebelum model dipakai serving.

Integrasi dan perhatian performa:
Toleransi numerik ditentukan per precision; nilai Recall@k/nDCG dan kualitas jawaban harus diperiksa terpisah.

Benchmark dan gate penerimaan:
Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
Bandingkan hasil terhadap referensi Python serta gold evidence; ekspor yang dapat
dimuat belum menjamin kesetaraan kualitas retrieval/reranking.

Status: reference generation and real native gRPC numeric parity CLI active.
Perhatian implementasi dan verifikasi:
Compare Python reference and actual C++ outputs using the same weights/tokenizer and representative batches.
Bukti verifikasi: Measure vector/score/rank deviations plus downstream quality, latency and memory; quantization requires evidence against unchanged gates.
Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
"""

import argparse
import hashlib
import json
import math
import re
import importlib.metadata
from pathlib import Path
import time
import uuid


def read_json_evidence(path, limit=64*1024*1024):
    with Path(path).open('rb') as stream: raw=stream.read(limit+1)
    if len(raw)>limit: raise ValueError('JSON evidence exceeds byte budget')
    value=json.loads(raw,parse_constant=lambda _: (_ for _ in ()).throw(ValueError('non-finite JSON')))
    return value,hashlib.sha256(raw).hexdigest(),raw


def read_json(path, limit=64*1024*1024):
    return read_json_evidence(path,limit)[0]


def validate_cases(cases):
    if not isinstance(cases,list) or not 1<=len(cases)<=10000:
        raise ValueError('bounded nonempty cases required')
    seen=set()
    for case in cases:
        if not isinstance(case,dict) or not isinstance(case.get('id'),str) or not 1<=len(case['id'])<=256 or any(ord(c)<33 or ord(c)>126 for c in case['id']) or case['id'] in seen:
            raise ValueError('unique string case IDs required')
        seen.add(case['id'])
        if not isinstance(case.get('text'),str) or not case['text'] or len(case['text'].encode())>1024*1024:
            raise ValueError('bounded nonempty text required')
        if 'query' in case and (not isinstance(case['query'],str) or not case['query'] or len(case['query'].encode())>1024*1024):
            raise ValueError('nonempty query required for rerank pairs')
    return cases


def load_cases(path):
    return validate_cases(read_json(path))


def validate_reference(document):
    """Refuse empty, malformed or non-finite evidence before making any native call."""
    from google.protobuf import json_format
    from regulagraph.v1 import common_pb2 as common
    from evaluation.datasets.schema import validate
    if document.get('schema_version')!=1: raise ValueError('unsupported reference schema')
    model=json_format.ParseDict(document['model'],common.ModelManifest());validate(model)
    if model.task not in (common.MODEL_TASK_EMBED,common.MODEL_TASK_RERANK): raise ValueError('unsupported reference task')
    if not re.fullmatch(r'[0-9a-f]{64}',document.get('manifest_sha256','')): raise ValueError('missing manifest pin')
    raw=document.get('manifest_json')
    if not isinstance(raw,str) or hashlib.sha256(raw.encode('utf-8')).hexdigest()!=document['manifest_sha256']:
        raise ValueError('reference manifest bytes/pin mismatch')
    if json_format.Parse(raw,common.ModelManifest())!=model: raise ValueError('reference model differs from pinned manifest')
    embed=model.task==common.MODEL_TASK_EMBED
    for case in validate_cases(document['cases']):
        if ('query' in case)==embed: raise ValueError('reference case task mismatch')
        ids=case.get('input_ids')
        if not isinstance(ids,list) or not 1<=len(ids)<=model.max_tokens or any(type(i)!=int or i<0 for i in ids):
            raise ValueError('invalid reference tokens')
        for name in ('fp32','serving_precision'):
            values=case.get(name)
            if not isinstance(values,list) or len(values)!=(model.dimensions if embed else 1) or any(type(x) not in (float,int) or not math.isfinite(x) for x in values):
                raise ValueError('invalid reference tensor')
            if embed and abs(sum(x*x for x in values)-1)>0.01: raise ValueError('reference vector is not normalized')
    return model


def validate_native_value(value,case,dimensions):
    """Validate embedding (dimensions > 0) or raw score and exact token accounting."""
    if value.input_tokens!=len(case['input_ids']) or value.truncation.truncated or value.truncation.retained_tokens!=len(case['input_ids']) or value.truncation.original_tokens!=len(case['input_ids']):
        raise ValueError('native token count or truncation mismatch')
    values=list(value.values) if dimensions else [value.score]
    if len(values)!=(dimensions or 1) or any(not math.isfinite(x) for x in values):
        raise ValueError('native output shape/finite violation')
    if dimensions and abs(sum(x*x for x in values)-1)>0.01:
        raise ValueError('native vector is not L2-normalized')
    return values


def prepare_reference(source,bundle,cases_path,output):
    """Record FP32 and serving-precision outputs before native sessions occupy the GPU."""
    import numpy as np
    import torch
    from transformers import AutoModel,AutoModelForSequenceClassification,AutoTokenizer
    from tooling.models.export import sha256,source_inventory
    if output.exists(): raise FileExistsError(output)
    model_meta,manifest_hash,manifest_bytes=read_json_evidence(bundle/'model.pbjson')
    export=read_json(bundle/'export.json')
    if manifest_hash!=export['manifest_sha256']: raise ValueError('bundle manifest drift')
    if model_meta['model_id']!=export['model_id'] or model_meta['version']!=export['revision']: raise ValueError('export identity mismatch')
    fixture=export.get('source_origin')=='SYNTHETIC_FIXTURE'
    if fixture and not export['model_id'].startswith('fixture:'): raise ValueError('invalid fixture identity')
    if source_inventory(source,export['revision'],fixture)!=export['source_hashes']:
        raise ValueError('reference source inventory differs from exported model')
    cases,cases_hash,_=read_json_evidence(cases_path);validate_cases(cases)
    embed=model_meta['task']=='MODEL_TASK_EMBED'
    if any(('query' in c)==embed for c in cases): raise ValueError('case task does not match model')
    tokenizer=AutoTokenizer.from_pretrained(source,local_files_only=True,trust_remote_code=False)
    loader=AutoModel if embed else AutoModelForSequenceClassification
    torch.set_num_threads(4)
    model=loader.from_pretrained(source,local_files_only=True,trust_remote_code=False,attn_implementation='eager').eval()
    device='cuda' if torch.cuda.is_available() else 'cpu'
    model.to(device)
    inputs=[]
    for case in cases:
        encoded=tokenizer(case['text'] if embed else case['query'],text_pair=None if embed else case['text'],return_tensors='pt',truncation=False)
        if encoded['input_ids'].shape[1]>model_meta['max_tokens']: raise ValueError('case exceeds pinned model capacity')
        inputs.append(encoded)
    def evaluate():
        results=[]
        with torch.inference_mode():
            for encoded in inputs:
                tensor=model(**{k:v.to(device) for k,v in encoded.items()})
                value=torch.nn.functional.normalize(tensor.last_hidden_state[:,0].float(),p=2,dim=1) if embed else tensor.logits.reshape(-1).float()
                results.append(value.reshape(-1).cpu().numpy().tolist())
        return results
    fp32=evaluate()
    if model_meta['precision']=='fp16': model.half();native_precision=evaluate()
    else: native_precision=fp32
    if source_inventory(source,export['revision'],fixture)!=export['source_hashes']:
        raise ValueError('reference source changed during inference')
    result={'schema_version':1,'model':model_meta,'manifest_sha256':export['manifest_sha256'],
            'manifest_json':manifest_bytes.decode('utf-8'),
            'cases_sha256':cases_hash,'source_hashes':export['source_hashes'],
            'status':'NUMERIC_REFERENCE_ONLY','device':device,
            'environment':{'toolchain':{p:importlib.metadata.version(p) for p in ('torch','transformers','tokenizers')},
                           'gpu':torch.cuda.get_device_name() if device=='cuda' else None,
                           'cuda':torch.version.cuda,'cudnn':torch.backends.cudnn.version(),
                           'matmul_tf32':torch.backends.cuda.matmul.allow_tf32,'cudnn_tf32':torch.backends.cudnn.allow_tf32},'cases':[
                {**case,'input_ids':encoded['input_ids'].reshape(-1).tolist(),'fp32':ref,'serving_precision':serving}
                for case,encoded,ref,serving in zip(cases,inputs,fp32,native_precision)]}
    output.parent.mkdir(parents=True,exist_ok=True)
    validate_reference(result)
    output.write_text(json.dumps(result,ensure_ascii=False,indent=2,allow_nan=False),encoding='utf-8')
    return {'reference':str(output),'sha256':sha256(output),'cases':len(cases),'device':device}


def compare_native(reference,endpoint,output,timeout=120,batch_size=8,token_probe=None):
    """Call the production C01 RPC; reports observed deviations, never a release PASS."""
    import grpc
    import numpy as np
    from google.protobuf import json_format
    from regulagraph.v1 import common_pb2 as common,inference_pb2 as wire
    from evaluation.datasets.schema import validate
    from tooling.models.export import sha256
    if output.exists(): raise FileExistsError(output)
    if not 1<=batch_size<=128 or not 0<timeout<=1800: raise ValueError('invalid workload bounds')
    document,reference_hash,_=read_json_evidence(reference)
    model=validate_reference(document)
    token_parity='NOT_MEASURED';probe_hash=None
    if token_probe is not None:
        probe,probe_hash,_=read_json_evidence(token_probe)
        expected={c['id']:c['input_ids'] for c in document['cases']}
        actual={c['id']:c['input_ids'] for c in probe}
        if len(probe)!=len(expected) or actual!=expected: raise ValueError('native token ID parity mismatch')
        token_parity='PASS'
    embed=model.task==common.MODEL_TASK_EMBED
    channel=grpc.insecure_channel(endpoint,options=[('grpc.max_receive_message_length',4*1024*1024)])
    request_type=wire.EmbedBatchRequest if embed else wire.RerankBatchRequest
    response_type=wire.EmbedBatchResponse if embed else wire.RerankBatchResponse
    rpc=channel.unary_unary('/regulagraph.v1.Inference/'+('EmbedBatch' if embed else 'RerankBatch'),
        request_serializer=request_type.SerializeToString,response_deserializer=response_type.FromString)
    observations=[];failures=[];latencies=[]
    def context():
        rc=common.RequestContext(schema_version=1,request_id=uuid.uuid4().hex,trace_id=uuid.uuid4().hex,
            corpus_id='corpus:parity',auth_scope_ref='local:model-verification')
        rc.deadline.FromMilliseconds(int((time.time()+timeout)*1000));rc.config_fingerprint.sha256=reference_hash;return rc
    for start in range(0,len(document['cases']),batch_size):
        cases=document['cases'][start:start+batch_size]
        request=request_type(context=context(),model=model,operation_key=uuid.uuid4().hex)
        if embed:
            request.purpose=wire.EMBEDDING_PURPOSE_DOCUMENT
            for case in cases: request.items.add(item_id=case['id'],text=case['text'])
        else:
            for case in cases: request.pairs.add(pair_id=case['id'],query=case['query'],text=case['text'])
        sent=time.perf_counter()
        try:
            response=rpc(request,timeout=timeout);elapsed=(time.perf_counter()-sent)*1000;latencies.append(elapsed)
            validate(response,max_items=128*4096+100000,max_bytes=4*1024*1024)
            if response.request_id!=request.context.request_id or response.model!=model: raise ValueError('native correlation/model mismatch')
            ids=[r.item_id if embed else r.pair_id for r in response.results]
            if len(ids)!=len(cases) or len(set(ids))!=len(cases) or set(ids)!={c['id'] for c in cases}: raise ValueError('result coverage mismatch')
            by_id=dict(zip(ids,response.results))
            for case in cases:
                item=by_id[case['id']]
                if item.HasField('error'): failures.append({'id':case['id'],'error':json_format.MessageToDict(item.error)});continue
                value=item.embedding if embed else item.score
                actual=np.asarray(validate_native_value(value,case,model.dimensions if embed else 0),dtype=np.float64)
                ref=np.asarray(case['fp32'],dtype=np.float64);same=np.asarray(case['serving_precision'],dtype=np.float64)
                if actual.shape!=ref.shape or not np.isfinite(actual).all(): raise ValueError('native output shape/finite violation')
                observations.append({'id':case['id'],'input_tokens':value.input_tokens,'values':actual.tolist(),
                    'max_abs_fp32':float(np.max(np.abs(actual-ref))),
                    'max_abs_same_precision':float(np.max(np.abs(actual-same))),
                    'cosine_fp32':float(np.dot(actual,ref)/(np.linalg.norm(actual)*np.linalg.norm(ref))) if embed else None})
        except Exception as exc:
            failures.append({'batch_start':start,'error':str(exc),'elapsed_ms':(time.perf_counter()-sent)*1000})
    channel.close()
    result={'status':'MEASURED_DIAGNOSTIC' if not failures else 'FAILED','required_gates':'NOT_MEASURED',
        'reference_sha256':reference_hash,'token_probe_sha256':probe_hash,'model':document['model'],'expected_items':len(document['cases']),
        'observed_items':len(observations),'failures':failures,'observations':observations,
        'client_batch_latency_ms':latencies,'batch_size':batch_size,'token_id_parity':token_parity,
        'quality_note':'Numeric diagnostic without human gold; not Recall/nDCG acceptance or reference-hardware load testing.'}
    output.parent.mkdir(parents=True,exist_ok=True);output.write_text(json.dumps(result,indent=2,allow_nan=False),encoding='utf-8')
    return {'status':result['status'],'items':len(observations),'failures':len(failures),'report':str(output)}


def main():
    p=argparse.ArgumentParser(description=__doc__);sub=p.add_subparsers(dest='command',required=True)
    prep=sub.add_parser('reference')
    for name in ('source','bundle','cases','output'):prep.add_argument('--'+name,type=Path,required=True)
    run=sub.add_parser('native');run.add_argument('--reference',type=Path,required=True);run.add_argument('--endpoint',required=True)
    run.add_argument('--output',type=Path,required=True);run.add_argument('--timeout',type=float,default=120);run.add_argument('--batch-size',type=int,default=8)
    run.add_argument('--token-probe',type=Path)
    args=vars(p.parse_args());command=args.pop('command')
    if command=='reference':args['cases_path']=args.pop('cases');result=prepare_reference(**args)
    else:result=compare_native(**args)
    print(json.dumps(result));return 1 if result.get('failures',0) else 0


if __name__=='__main__':raise SystemExit(main())

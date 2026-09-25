"""Open-loop diagnostic load against the actual native C01 service.

Freezes a numeric reference and records every offered arrival, including local capacity
rejections and RPC/item errors, with queue-inclusive latency. This isolated model run
does not establish full retrieval/quality or reference-hardware acceptance. Required
targets remain configs/benchmark-targets.yaml; this tool never assigns release PASS.
"""
import argparse
import concurrent.futures
import json
import math
from pathlib import Path
import threading
import time
from tooling.models.parity import read_json_evidence,validate_reference,validate_native_value


def run(reference,endpoint,output,requests,rps,max_in_flight,deadline_ms,purpose,batch_size):
    import grpc
    from regulagraph.v1 import common_pb2 as common,inference_pb2 as wire
    from evaluation.datasets.schema import validate
    if output.exists(): raise FileExistsError(output)
    if not 1<=requests<=100000 or not 0<rps<=10000 or not 1<=max_in_flight<=128 or not 1<=deadline_ms<=1800000 or not 1<=batch_size<=128:
        raise ValueError('invalid bounded diagnostic workload')
    document,fingerprint,_=read_json_evidence(reference);model=validate_reference(document);cases=document['cases']
    embed=model.task==common.MODEL_TASK_EMBED
    request_type=wire.EmbedBatchRequest if embed else wire.RerankBatchRequest
    response_type=wire.EmbedBatchResponse if embed else wire.RerankBatchResponse
    channel=grpc.insecure_channel(endpoint,options=[('grpc.max_receive_message_length',4*1024*1024)])
    rpc=channel.unary_unary('/regulagraph.v1.Inference/'+('EmbedBatch' if embed else 'RerankBatch'),
        request_serializer=request_type.SerializeToString,response_deserializer=response_type.FromString)
    origin=time.perf_counter();gate=threading.BoundedSemaphore(max_in_flight)
    rows=[];futures=[]
    def invoke(index,due):
        sent=time.perf_counter();row={'arrival':index,'scheduled_ms':(due-origin)*1000,'dispatch_lag_ms':(sent-due)*1000}
        try:
            remaining=deadline_ms/1000-(sent-due)
            if remaining<=0:raise TimeoutError('arrival deadline expired before dispatch')
            context=common.RequestContext(schema_version=1,request_id=f'load:{index}',trace_id=f'trace:{index}',
                corpus_id='corpus:diagnostic',auth_scope_ref='local:diagnostic')
            context.deadline.FromMilliseconds(int((time.time()+remaining)*1000));context.config_fingerprint.sha256=fingerprint
            request=request_type(context=context,model=model,operation_key=f'operation:{index}')
            chosen=[cases[(index*batch_size+j)%len(cases)] for j in range(batch_size)]
            if embed:
                request.purpose=wire.EMBEDDING_PURPOSE_QUERY if purpose=='query' else wire.EMBEDDING_PURPOSE_DOCUMENT
                for j,c in enumerate(chosen):request.items.add(item_id=f'item:{j}',text=c['text'])
            else:
                for j,c in enumerate(chosen):request.pairs.add(pair_id=f'item:{j}',query=c['query'],text=c['text'])
            response=rpc(request,timeout=remaining)
            validate(response,max_items=128*4096+100000,max_bytes=4*1024*1024)
            ids=[i.item_id if embed else i.pair_id for i in response.results]
            if response.request_id!=context.request_id or response.model!=model or len(ids)!=batch_size or set(ids)!={f'item:{j}' for j in range(batch_size)}:
                raise ValueError('native response correlation mismatch')
            errors=[i.error.safe_message for i in response.results if i.HasField('error')]
            by_id=dict(zip(ids,response.results))
            for j,case in enumerate(chosen):
                item=by_id[f'item:{j}']
                if item.HasField('error'):continue
                validate_native_value(item.embedding if embed else item.score,case,model.dimensions if embed else 0)
            row.update(status='ITEM_ERROR' if errors else 'OK',errors=errors,
                input_tokens=sum((i.embedding.input_tokens if embed else i.score.input_tokens) for i in response.results if not i.HasField('error')),
                durations=[{'stage':d.stage,'duration_ns':d.duration_ns,'queue_ns':d.queue_ns} for d in response.durations])
        except Exception as error:row.update(status='RPC_ERROR',error=str(error))
        finally:
            row['latency_ms']=(time.perf_counter()-due)*1000;gate.release()
        return row
    try:
        with concurrent.futures.ThreadPoolExecutor(max_workers=max_in_flight) as pool:
            for index in range(requests):
                due=origin+index/rps;delay=due-time.perf_counter()
                if delay>0:time.sleep(delay)
                if gate.acquire(blocking=False):futures.append(pool.submit(invoke,index,due))
                else:rows.append({'arrival':index,'scheduled_ms':(due-origin)*1000,'status':'LOCAL_CAPACITY_REJECTED','latency_ms':(time.perf_counter()-due)*1000})
            rows.extend(f.result() for f in futures)
    finally:channel.close()
    rows.sort(key=lambda r:r['arrival']);elapsed=time.perf_counter()-origin
    assert len(rows)==requests
    ordered=sorted(r['latency_ms'] for r in rows)
    completed=sorted(r['latency_ms'] if r['status']=='OK' else math.inf for r in rows)
    def percentile(values,p):return values[min(len(values)-1,math.ceil(p*len(values))-1)]
    quantiles={name:percentile(completed,p) for name,p in [('p50',.5),('p95',.95),('p99',.99)]}
    result={'status':'MEASURED_DIAGNOSTIC' if all(r['status']=='OK' for r in rows) else 'FAILED_DIAGNOSTIC','required_gates':'NOT_MEASURED','reference_sha256':fingerprint,
        'model':document['model'],'workload':{'requests':requests,'rps':rps,'max_in_flight':max_in_flight,'deadline_ms':deadline_ms,'batch_size':batch_size,'purpose':purpose},
        'elapsed_s':elapsed,'completed_ok':sum(r['status']=='OK' for r in rows),'observations':rows,
        'time_to_outcome_all_arrivals_ms':{'p50':percentile(ordered,.5),'p95':percentile(ordered,.95),'p99':percentile(ordered,.99)},
        'completion_latency_all_arrivals_ms':{name:None if math.isinf(value) else value for name,value in quantiles.items()},
        'completion_quantile_status':{name:'INFINITE_NONCOMPLETION' if math.isinf(value) else 'FINITE' for name,value in quantiles.items()},
        'note':'Noncompleted arrivals have infinite completion time (JSON null plus explicit status). Time-to-outcome includes quick failures and is not completion latency. This diagnostic is not a release gate.'}
    output.parent.mkdir(parents=True,exist_ok=True);output.write_text(json.dumps(result,indent=2,allow_nan=False),encoding='utf-8')
    return {'report':str(output),'offered':requests,'completed_ok':result['completed_ok'],'status':result['status']}


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--reference',type=Path,required=True);p.add_argument('--endpoint',required=True);p.add_argument('--output',type=Path,required=True)
    p.add_argument('--requests',type=int,required=True);p.add_argument('--rps',type=float,required=True);p.add_argument('--max-in-flight',type=int,default=32)
    p.add_argument('--deadline-ms',type=int,default=2000);p.add_argument('--purpose',choices=('query','document'),default='query');p.add_argument('--batch-size',type=int,default=1)
    result=run(**vars(p.parse_args()));print(json.dumps(result));return int(result['completed_ok']!=result['offered'])


if __name__=='__main__':raise SystemExit(main())

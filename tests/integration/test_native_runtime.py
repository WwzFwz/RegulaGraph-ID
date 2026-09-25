"""Run C++ startup integrity and concurrent C01 requests against real ONNX fixture sessions.

Set REGULAGRAPH_NATIVE_BINARY to opt in. Uses bounded local child processes and loopback
ports; no downloads/deployment. Tests admission, old deadlines, partial results and model
sidecar integrity. Fixture timings never substitute for production load/quality gates.
"""
import concurrent.futures
import json
import os
from pathlib import Path
import socket
import subprocess
import threading
import time
import pytest
from native_fixture import create
from tooling.models.export import sha256


def binary():
    value=os.environ.get('REGULAGRAPH_NATIVE_BINARY')
    if not value: pytest.skip('REGULAGRAPH_NATIVE_BINARY required')
    return str(Path(value).resolve())


def context():
    from regulagraph.v1 import common_pb2 as wire
    result=wire.RequestContext(schema_version=1,request_id='test:native',trace_id='test:trace',
        corpus_id='test:corpus',auth_scope_ref='local:test')
    result.config_fingerprint.sha256='a'*64;result.deadline.FromMilliseconds(int((time.time()+60)*1000))
    return result


def command(bundle,port):
    return [binary(),'--bundle',str(bundle),'--manifest-sha256',sha256(bundle/'model.pbjson'),
            '--listen','127.0.0.1:'+str(port)]


@pytest.fixture
def session(tmp_path,request):
    import grpc
    from google.protobuf.json_format import Parse
    from regulagraph.v1 import common_pb2 as common,inference_pb2 as wire
    binary();bundle=Path(create(tmp_path,'embed','1.22.0')['bundle'])
    if getattr(request,'param',None)=='zero_on_izin':
        import onnx
        from onnx import helper as h,TensorProto as t
        model=onnx.load(str(bundle/'model.onnx'))
        model.graph.node[-1].output[0]='raw_embedding'
        model.graph.initializer.extend([h.make_tensor('eleven',t.FLOAT,[],[11]),h.make_tensor('zero',t.FLOAT,[],[0]),h.make_tensor('one',t.FLOAT,[],[1])])
        model.graph.node.extend([h.make_node('Equal',['sum','eleven'],['bad']),h.make_node('Where',['bad','zero','one'],['factor']),
            h.make_node('Unsqueeze',['factor','axes'],['factor_column']),h.make_node('Mul',['raw_embedding','factor_column'],['embedding'])])
        onnx.save(model,str(bundle/'model.onnx'));repin(bundle,False)
    with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
    log=(tmp_path/'server.log').open('w')
    process=subprocess.Popen(command(bundle,port),stdout=log,stderr=log,
        creationflags=subprocess.CREATE_NO_WINDOW if os.name=='nt' else 0)
    channel=grpc.insecure_channel('127.0.0.1:'+str(port))
    try:
        grpc.channel_ready_future(channel).result(timeout=20)
        model=Parse((bundle/'model.pbjson').read_text(),common.ModelManifest())
        rpc=channel.unary_unary('/regulagraph.v1.Inference/EmbedBatch',
            request_serializer=wire.EmbedBatchRequest.SerializeToString,response_deserializer=wire.EmbedBatchResponse.FromString)
        caps=channel.unary_unary('/regulagraph.v1.Inference/GetCapabilities',
            request_serializer=wire.CapabilitiesRequest.SerializeToString,response_deserializer=wire.CapabilitiesResponse.FromString)
        def request(bulk=False,count=1):
            value=wire.EmbedBatchRequest(context=context(),model=model,operation_key='test:embed',purpose=2 if bulk else 1)
            for i in range(count):value.items.add(item_id='item:'+str(i),text='izin usaha' if not bulk else 'izin '*4096)
            return value
        yield rpc,request,process,caps
    finally:
        channel.close()
        if process.poll() is None:process.terminate()
        process.wait(timeout=10);log.close()


def test_ancient_deadline_and_expired_request_are_rejected(session):
    import grpc
    rpc,request,_,_=session
    for seconds in (-11644473600,int(time.time())-1):
        value=request();value.context.deadline.seconds=seconds;value.context.deadline.nanos=0
        with pytest.raises(grpc.RpcError) as error:rpc(value,timeout=2)
        assert error.value.code()==grpc.StatusCode.DEADLINE_EXCEEDED
    assert rpc(request(),timeout=2).results[0].HasField('embedding')


def test_bulk_saturation_keeps_online_admission_available(session):
    import grpc
    rpc,request,process,_=session;start=threading.Barrier(9)
    def bulk(_):
        start.wait();codes=[]
        for _ in range(5):
            try:rpc(request(True,96),timeout=30);codes.append('accepted')
            except grpc.RpcError as e:codes.append(e.code().name)
        return codes
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        futures=[pool.submit(bulk,i) for i in range(8)];start.wait()
        for _ in range(20):assert rpc(request(),timeout=30).results[0].HasField('embedding')
        results=[code for f in futures for code in f.result()]
    assert 'accepted' in results and 'RESOURCE_EXHAUSTED' in results
    assert set(results)<={'accepted','RESOURCE_EXHAUSTED'} and process.poll() is None


def test_cancelled_bulk_does_not_poison_shared_session(session):
    rpc,request,process,_=session
    pending=rpc.future(request(True,96),timeout=30);time.sleep(0.01);assert pending.cancel()
    assert rpc(request(),timeout=5).results[0].HasField('embedding') and process.poll() is None


@pytest.mark.parametrize('session',['zero_on_izin'],indirect=True)
def test_model_execution_failure_clears_readiness_and_stops_new_work(session):
    import grpc
    from regulagraph.v1 import inference_pb2 as wire
    rpc,request,_,caps=session
    assert caps(wire.CapabilitiesRequest(context=context()),timeout=2).ready
    broken=request();broken.items[0].text='izin'
    result=rpc(broken,timeout=2)
    assert result.results[0].HasField('error')
    readiness=caps(wire.CapabilitiesRequest(context=context()),timeout=2)
    assert not readiness.ready and all(not m.ready for m in readiness.models)
    with pytest.raises(grpc.RpcError) as error:rpc(request(),timeout=2)
    assert error.value.code()==grpc.StatusCode.UNAVAILABLE


def external_bundle(tmp_path):
    import onnx,numpy as np
    from onnx import numpy_helper
    bundle=Path(create(tmp_path,'embed','1.22.0')['bundle'])
    graph=onnx.load(str(bundle/'model.onnx'))
    graph.graph.initializer.append(numpy_helper.from_array(np.asarray([0.0],dtype=np.float32),name='bias'))
    graph.graph.node[1].output[0]='float_raw'
    graph.graph.node.insert(2,onnx.helper.make_node('Add',['float_raw','bias'],['float']))
    onnx.save_model(graph,str(bundle/'model.onnx'),save_as_external_data=True,all_tensors_to_one_file=True,
                    location='model.onnx.data',size_threshold=0)
    return bundle


def repin(bundle,include_sidecar):
    weights={'model.onnx':sha256(bundle/'model.onnx')}
    if include_sidecar:weights['model.onnx.data']=sha256(bundle/'model.onnx.data')
    (bundle/'weights.json').write_text(json.dumps(weights))
    manifest=json.loads((bundle/'model.pbjson').read_text());manifest['weights_hash']['sha256']=sha256(bundle/'weights.json')
    (bundle/'model.pbjson').write_text(json.dumps(manifest))


@pytest.mark.parametrize('mode',['missing','tampered','valid'])
def test_external_weights_must_be_in_catalog_and_match_hash(tmp_path,mode):
    binary();bundle=external_bundle(tmp_path);repin(bundle,mode!='missing')
    if mode=='tampered':(bundle/'model.onnx.data').write_bytes(b'changed!')
    # token_probe uses the same runtime startup verifier without opening a listener.
    probe=Path(binary()).with_name('regulagraph_token_probe'+('.exe' if os.name=='nt' else ''))
    cases=tmp_path/'cases.json';cases.write_text('[{"id":"one","text":"izin"}]')
    result=subprocess.run([str(probe),str(bundle),sha256(bundle/'model.pbjson'),str(cases),str(tmp_path/'tokens.json')],
        capture_output=True,text=True,timeout=30,creationflags=subprocess.CREATE_NO_WINDOW if os.name=='nt' else 0)
    assert (result.returncode==0)==(mode=='valid'),result.stderr
    if mode=='missing':assert 'unpinned ONNX dependency' in result.stderr
    if mode=='tampered':assert 'model weights hash mismatch' in result.stderr

"""Reject model source drift and malformed numerical evidence before expensive ML work.

Fixtures test provenance/denominator integrity only; no synthetic result proves model
quality or latency. All official sources must match immutable download receipts.
"""
import copy
import hashlib
import json
import pytest
from tooling.models.export import source_inventory
from tooling.models.parity import load_cases, validate_reference


def test_source_receipts_bind_revision_and_bytes(tmp_path):
    source=tmp_path/'source';source.mkdir()
    receipt=source/'.cache/huggingface/download';receipt.mkdir(parents=True)
    for name,content in [('config.json',b'{}'),('model.safetensors',b'weights')]:
        (source/name).write_bytes(content)
        etag=hashlib.sha256(content).hexdigest() if name.endswith('safetensors') else hashlib.sha1(b'blob 2\0{}').hexdigest()
        (receipt/(name+'.metadata')).write_text('a'*40+'\n'+etag+'\n0')
    expected=source_inventory(source,'a'*40)
    assert len(expected)==2
    with pytest.raises(ValueError,match='revision'):source_inventory(source,'b'*40)
    (source/'model.safetensors').write_bytes(b'changed')
    with pytest.raises(ValueError,match='content'):source_inventory(source,'a'*40)
    assert source_inventory(source,'a'*40,True)!=expected
    (source/'extra.json').write_text('{}')
    with pytest.raises(ValueError,match='receipt'):source_inventory(source,'a'*40)


@pytest.mark.parametrize('case',[{'id':'','text':'x'},{'id':'has space','text':'x'},
    {'id':'x','text':'x','query':'q'*(1024*1024+1)}])
def test_case_input_bounds(tmp_path,case):
    path=tmp_path/'cases.json';path.write_text(json.dumps([case]))
    with pytest.raises(ValueError):load_cases(path)


def reference():
    result={'schema_version':1,'model':{
        'model_id':'fixture:embed','version':'a'*40,'task':'MODEL_TASK_EMBED',
        'weights_hash':{'sha256':'b'*64},'tokenizer_hash':{'sha256':'c'*64},
        'dimensions':2,'pooling':'cls','normalization':'l2','max_tokens':64,
        'precision':'fp32','backend':'onnxruntime:1.22.0:cpu'},
        'cases':[{'id':'formal','text':'izin','input_ids':[2,4,3],
                  'fp32':[1.0,0.0],'serving_precision':[1.0,0.0]}]}
    result['manifest_json']=json.dumps(result['model'])
    result['manifest_sha256']=hashlib.sha256(result['manifest_json'].encode()).hexdigest()
    return result


def test_reference_refuses_empty_nonfinite_dimension_and_token_corruption():
    good=reference();validate_reference(good)
    bads=[]
    bad=copy.deepcopy(good);bad['cases']=[];bads.append(bad)
    bad=copy.deepcopy(good);bad['cases'][0]['fp32'][0]=float('nan');bads.append(bad)
    bad=copy.deepcopy(good);bad['cases'][0]['serving_precision']=[1.0];bads.append(bad)
    bad=copy.deepcopy(good);bad['cases'][0]['input_ids']=[-1];bads.append(bad)
    bad=copy.deepcopy(good);bad['cases']*=2;bads.append(bad)
    bad=copy.deepcopy(good);bad['model']['version']='other';bads.append(bad)
    for bad in bads:
        with pytest.raises(ValueError):validate_reference(bad)


def test_json_rejects_nonfinite(tmp_path):
    path=tmp_path/'bad.json';path.write_text('[{"id":"x","text":"x","value":NaN}]')
    with pytest.raises(ValueError):load_cases(path)


def test_native_parity_refuses_duplicate_results(tmp_path,monkeypatch):
    import grpc
    from regulagraph.v1 import inference_pb2 as wire
    from tooling.models.parity import compare_native
    class Channel:
        def unary_unary(self,*args,**kwargs):
            def rpc(request,timeout):
                result=wire.EmbedBatchResponse(request_id=request.context.request_id,model=request.model)
                for _ in range(2):
                    item=result.results.add(item_id='formal');item.embedding.values.extend([1.0,0.0])
                    item.embedding.input_tokens=3;item.embedding.truncation.original_tokens=3;item.embedding.truncation.retained_tokens=3
                return result
            return rpc
        def close(self):pass
    monkeypatch.setattr(grpc,'insecure_channel',lambda *a,**k:Channel())
    path=tmp_path/'ref.json';path.write_text(json.dumps(reference()))
    report=tmp_path/'out.json'
    result=compare_native(path,'unused',report)
    assert result['status']=='FAILED' and result['failures']==1
    assert 'coverage mismatch' in report.read_text()


def test_native_vector_validation_rejects_zero_and_wrong_token_accounting():
    from regulagraph.v1 import inference_pb2 as wire
    from tooling.models.parity import validate_native_value
    value=wire.Embedding(values=[0.0,0.0],input_tokens=3)
    value.truncation.original_tokens=3;value.truncation.retained_tokens=3
    case={'input_ids':[2,4,3]}
    with pytest.raises(ValueError,match='normalized'):validate_native_value(value,case,2)
    value.values[0]=1.0;validate_native_value(value,case,2)
    value.truncation.original_tokens=4
    with pytest.raises(ValueError,match='token count'):validate_native_value(value,case,2)


def test_evidence_hash_is_bound_to_the_bytes_read(tmp_path):
    from tooling.models.parity import read_json_evidence
    path=tmp_path/'cases.json';raw=b'{"generation":1}';path.write_bytes(raw)
    data,digest,frozen=read_json_evidence(path)
    path.write_text('{"generation":2}')
    assert data=={'generation':1} and frozen==raw and digest==hashlib.sha256(raw).hexdigest()


def test_load_retains_capacity_rejections_and_original_reference_hash(tmp_path,monkeypatch):
    import grpc,time
    from regulagraph.v1 import inference_pb2 as wire
    from tooling.models.load import run
    path=tmp_path/'reference.json';path.write_text(json.dumps(reference()))
    initial_hash=hashlib.sha256(path.read_bytes()).hexdigest()
    class Channel:
        def unary_unary(self,*args,**kwargs):
            def rpc(request,timeout):
                path.write_text('{}');time.sleep(0.03)
                result=wire.EmbedBatchResponse(request_id=request.context.request_id,model=request.model)
                item=result.results.add(item_id='item:0');item.embedding.values.extend([1.0,0.0])
                item.embedding.input_tokens=3;item.embedding.truncation.original_tokens=3;item.embedding.truncation.retained_tokens=3
                return result
            return rpc
        def close(self):pass
    monkeypatch.setattr(grpc,'insecure_channel',lambda *a,**k:Channel())
    output=tmp_path/'load.json'
    run(path,'unused',output,20,10000,1,1000,'query',1)
    report=json.loads(output.read_text())
    assert report['reference_sha256']==initial_hash
    assert len(report['observations'])==20 and report['completed_ok']>=1
    assert any(row['status']=='LOCAL_CAPACITY_REJECTED' for row in report['observations'])
    assert report['required_gates']=='NOT_MEASURED'
    assert report['completion_latency_all_arrivals_ms']['p95'] is None
    assert report['completion_quantile_status']['p95']=='INFINITE_NONCOMPLETION'
    assert report['status']=='FAILED_DIAGNOSTIC'

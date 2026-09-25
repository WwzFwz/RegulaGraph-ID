"""Generate tiny real ONNX/tokenizer bundles for native boundary integration tests.

The graphs have deterministic synthetic weights and exercise actual tokenization, tensor
execution, gRPC, and model pin validation. They never establish BGE quality or performance.
Artifacts live in the supplied new test directory and use the production exporter bundle shape.
"""
import argparse
import hashlib
import json
from pathlib import Path


def create(root:Path,task:str,version:str):
    import onnx
    from onnx import helper as h,TensorProto as t
    from tokenizers import Tokenizer,models,pre_tokenizers,processors
    from google.protobuf.json_format import MessageToDict
    from regulagraph.v1 import common_pb2 as wire
    from tooling.models.export import sha256
    directory=root/task;directory.mkdir(parents=True,exist_ok=False)
    vocab={'[UNK]':0,'[PAD]':1,'[CLS]':2,'[SEP]':3,'Pasal':4,'1':5,'izin':6,'usaha':7,'.':8}
    tokenizer=Tokenizer(models.WordLevel(vocab,unk_token='[UNK]'))
    tokenizer.pre_tokenizer=pre_tokenizers.Whitespace()
    tokenizer.post_processor=processors.TemplateProcessing(single='[CLS] $A [SEP]',pair='[CLS] $A [SEP] $B [SEP]',special_tokens=[('[CLS]',2),('[SEP]',3)])
    tokenizer.save(str(directory/'tokenizer.json'))
    nodes=[h.make_node('Mul',['input_ids','attention_mask'],['masked']),
           h.make_node('Cast',['masked'],['float'],to=t.FLOAT),
           h.make_node('ReduceSum',['float','axes'],['sum'],keepdims=0)]
    if task=='embed':
        nodes += [h.make_node('Cos',['sum'],['cos']),h.make_node('Sin',['sum'],['sin']),
                  h.make_node('Unsqueeze',['cos','axes'],['a']),h.make_node('Unsqueeze',['sin','axes'],['b']),
                  h.make_node('Concat',['a','b'],['embedding'],axis=1)]
        output=h.make_tensor_value_info('embedding',t.FLOAT,['batch',2])
    else:
        nodes += [h.make_node('Identity',['sum'],['scores'])]
        output=h.make_tensor_value_info('scores',t.FLOAT,['batch'])
    graph=h.make_graph(nodes,'synthetic-inference-fixture',[
        h.make_tensor_value_info('input_ids',t.INT64,['batch','tokens']),
        h.make_tensor_value_info('attention_mask',t.INT64,['batch','tokens'])],[output],
        [h.make_tensor('axes',t.INT64,[1],[1])])
    model=h.make_model(graph,opset_imports=[h.make_opsetid('',17)]);model.ir_version=10
    h.set_model_props(model,{'regulagraph.pad_token_id':'1','regulagraph.task':task})
    onnx.save(model,str(directory/'model.onnx'))
    (directory/'weights.json').write_text(json.dumps({'model.onnx':sha256(directory/'model.onnx')},sort_keys=True,separators=(',',':')))
    manifest=wire.ModelManifest(model_id='fixture:'+task,version='synthetic-v1',task=wire.MODEL_TASK_EMBED if task=='embed' else wire.MODEL_TASK_RERANK,
        pooling='cls' if task=='embed' else 'logit',normalization='l2' if task=='embed' else 'none',max_tokens=8192,
        precision='fp32',backend='onnxruntime:'+version+':cpu')
    manifest.weights_hash.sha256=sha256(directory/'weights.json');manifest.tokenizer_hash.sha256=sha256(directory/'tokenizer.json')
    if task=='embed':manifest.dimensions=2
    (directory/'model.pbjson').write_text(json.dumps(MessageToDict(manifest,preserving_proto_field_name=True),sort_keys=True,indent=2))
    return {'bundle':str(directory),'manifest_sha256':sha256(directory/'model.pbjson')}


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--output',type=Path,required=True);parser.add_argument('--runtime-version',default='1.22.0')
    args=parser.parse_args();print(json.dumps([create(args.output,t,args.runtime_version) for t in ('embed','rerank')],indent=2))

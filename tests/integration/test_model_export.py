"""Exercise model export against tiny local Transformers/ONNX engines, without network.

Random XLM-RoBERTa weights and a deterministic tokenizer exercise both output contracts,
dynamic padding and manifest integrity. These are compatibility fixtures, not model gold.
"""
import hashlib
import json
from pathlib import Path
import numpy as np
import pytest


def tiny_source(directory:Path,task:str):
    torch=pytest.importorskip('torch')
    from transformers import XLMRobertaConfig,XLMRobertaModel,XLMRobertaForSequenceClassification,PreTrainedTokenizerFast
    from tokenizers import Tokenizer,models,pre_tokenizers,processors
    tokenizer=Tokenizer(models.WordLevel({'[UNK]':0,'[PAD]':1,'[CLS]':2,'[SEP]':3,'izin':4,'usaha':5,'Pasal':6,'1':7,'.':8},unk_token='[UNK]'))
    tokenizer.pre_tokenizer=pre_tokenizers.Whitespace()
    tokenizer.post_processor=processors.TemplateProcessing(single='[CLS] $A [SEP]',pair='[CLS] $A [SEP] $B [SEP]',special_tokens=[('[CLS]',2),('[SEP]',3)])
    fast=PreTrainedTokenizerFast(tokenizer_object=tokenizer,unk_token='[UNK]',pad_token='[PAD]',cls_token='[CLS]',sep_token='[SEP]')
    fast.save_pretrained(directory)
    torch.manual_seed(13)
    config=XLMRobertaConfig(vocab_size=9,hidden_size=8,num_hidden_layers=1,num_attention_heads=2,
                           intermediate_size=16,max_position_embeddings=66,pad_token_id=1,num_labels=1)
    model=XLMRobertaModel(config) if task=='embed' else XLMRobertaForSequenceClassification(config)
    model.eval().save_pretrained(directory)
    return model,fast


@pytest.mark.parametrize('task',['embed','rerank'])
def test_export_matches_reference_and_pins_files(tmp_path,task):
    torch=pytest.importorskip('torch');ort=pytest.importorskip('onnxruntime')
    from tooling.models.export import export_model,sha256
    source=tmp_path/'source';source.mkdir();model,tokenizer=tiny_source(source,task)
    output=tmp_path/'bundle'
    metadata=export_model(source,output,'fixture:'+task,'a'*40,task,'fp32','cpu',64,local_fixture=True)
    manifest=json.loads((output/'model.pbjson').read_text())
    assert metadata['manifest_sha256']==sha256(output/'model.pbjson')
    assert manifest['weights_hash']['sha256']==sha256(output/'weights.json')
    assert manifest['tokenizer_hash']['sha256']==sha256(output/'tokenizer.json')
    for name,digest in json.loads((output/'weights.json').read_text()).items():assert sha256(output/name)==digest
    session=ort.InferenceSession(str(output/'model.onnx'),providers=['CPUExecutionProvider'])
    for texts in [['izin'],['izin usaha','Pasal 1 .']]:
        inputs=tokenizer(texts,text_pair=None if task=='embed' else ['izin']*len(texts),padding=True,return_tensors='pt',return_token_type_ids=False)
        with torch.inference_mode():
            result=model(**inputs)
            expected=torch.nn.functional.normalize(result.last_hidden_state[:,0].float(),dim=1) if task=='embed' else result.logits.reshape(-1).float()
        actual=session.run(None,{k:v.numpy() for k,v in inputs.items()})[0]
        np.testing.assert_allclose(actual,expected.numpy(),rtol=1e-4,atol=1e-5)
    with pytest.raises(FileExistsError):export_model(source,output,'fixture:'+task,'a'*40,task,'fp32','cpu',64)


def test_export_rejects_unpinned_or_unsupported_profiles(tmp_path):
    from tooling.models.export import export_model
    for revision,precision,provider,max_tokens in [('main','fp32','cpu',64),('a'*40,'int8','cpu',64),('a'*40,'fp16','cpu',64),('a'*40,'fp32','cpu',0)]:
        with pytest.raises(ValueError):export_model(tmp_path,tmp_path/'out','fixture',revision,'embed',precision,provider,max_tokens)
    assert not (tmp_path/'out').exists()

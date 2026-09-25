"""
Mengekspor model kandidat ke format yang didukung runtime native.

Peran dalam komponen:
Menyiapkan artefak embedding/reranker beserta metadata untuk src/inference.

Integrasi dan perhatian performa:
Backend ONNX Runtime 1.22.0. Simpan tokenizer, pooling, output normalization, shape, precision, dan model hash.

Benchmark dan gate penerimaan:
Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
Bandingkan hasil terhadap referensi Python serta gold evidence; ekspor yang dapat
dimuat belum menjamin kesetaraan kualitas retrieval/reranking.

Status: export CLI aktif untuk dense CLS/L2 dan raw cross-encoder logits XLM-RoBERTa.
Perhatian implementasi dan verifikasi:
Export selected model plus tokenizer/pooling/normalization/precision/dynamic-shape manifest and content hashes.
Bukti verifikasi: Test unsupported operations and representative multilingual/long inputs; export success alone cannot authorize production parity.
Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
"""

import argparse
import hashlib
import importlib.metadata
import json
from pathlib import Path
import re


def sha256(path: Path) -> str:
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def source_inventory(source: Path, revision: str, local_fixture: bool = False) -> dict:
    """Bind local loader inputs to HF commit receipts; fixtures are explicitly non-upstream.

    HF receipts are preparation evidence, not signatures. The trusted downloader must
    fetch an immutable revision; each recorded ETag is checked against actual bytes.
    """
    files = sorted(p for p in source.iterdir() if p.is_file() and
                   p.suffix in ('.bin', '.safetensors', '.json', '.model'))
    if not files or not (source / 'config.json').is_file():
        raise ValueError('model source/config missing')
    result = {}
    for path in files:
        digest = sha256(path)
        if not local_fixture:
            receipt = source / '.cache/huggingface/download' / (path.name + '.metadata')
            if not receipt.is_file(): raise ValueError('missing upstream receipt: ' + path.name)
            lines = receipt.read_text(encoding='utf-8').splitlines()
            if len(lines) < 2 or lines[0] != revision:
                raise ValueError('source revision mismatch: ' + path.name)
            etag = lines[1]
            if re.fullmatch(r'[0-9a-f]{64}', etag): actual = digest
            elif re.fullmatch(r'[0-9a-f]{40}', etag):
                blob = hashlib.sha1(('blob ' + str(path.stat().st_size) + '\0').encode())
                with path.open('rb') as stream:
                    while block := stream.read(1024 * 1024): blob.update(block)
                actual = blob.hexdigest()
            else: raise ValueError('unsupported upstream ETag')
            if actual != etag: raise ValueError('source content mismatch: ' + path.name)
        result[path.name] = digest
    return result


def export_model(source: Path, output: Path, model_id: str, revision: str,
                 task: str, precision: str, provider: str, max_tokens: int,
                 local_fixture: bool = False) -> dict:
    """Export a local pinned source; output must be new to prevent mixed generations."""
    if not re.fullmatch(r'[0-9a-f]{40}', revision):
        raise ValueError('revision must be the exact upstream commit SHA')
    if task not in ('embed', 'rerank') or precision not in ('fp32', 'fp16'):
        raise ValueError('unsupported task or precision')
    if provider not in ('cpu', 'cuda') or not 4 <= max_tokens <= 8192:
        raise ValueError('unsupported provider or token budget')
    if precision == 'fp16' and provider != 'cuda':
        raise ValueError('FP16 export requires the CUDA serving profile')
    if output.exists():
        raise FileExistsError(output)
    if local_fixture and not model_id.startswith('fixture:'):
        raise ValueError('synthetic source requires fixture: model identity')
    source_hashes = source_inventory(source, revision, local_fixture)
    import torch
    import onnx
    from transformers import AutoModel, AutoModelForSequenceClassification, AutoTokenizer
    from google.protobuf.json_format import MessageToDict
    from regulagraph.v1 import common_pb2 as wire
    from evaluation.datasets.schema import validate

    # Read-only local source: downloads/revision resolution are explicit preparation steps.
    tokenizer = AutoTokenizer.from_pretrained(source, local_files_only=True, use_fast=True, trust_remote_code=False)
    loader = AutoModel if task == 'embed' else AutoModelForSequenceClassification
    model = loader.from_pretrained(source, local_files_only=True, trust_remote_code=False,
                                   attn_implementation='eager').eval()
    if model.config.model_type != 'xlm-roberta':
        raise ValueError('this exporter supports the pinned XLM-RoBERTa family only')
    if tokenizer.pad_token_id is None:
        raise ValueError('tokenizer must define padding')
    if max_tokens > model.config.max_position_embeddings - tokenizer.pad_token_id - 1:
        raise ValueError('max_tokens exceeds model positional capacity')
    if task == 'rerank' and model.config.num_labels != 1:
        raise ValueError('reranking requires one uncalibrated relevance logit')
    if precision == 'fp16':
        model.half()

    class DenseOrScore(torch.nn.Module):
        def __init__(self, inner):
            super().__init__()
            self.inner = inner

        def forward(self, input_ids, attention_mask):
            result = self.inner(input_ids=input_ids, attention_mask=attention_mask, return_dict=True)
            if task == 'embed':
                return torch.nn.functional.normalize(result.last_hidden_state[:, 0].float(), p=2, dim=1)
            return result.logits.reshape(-1).float()

    output.mkdir(parents=True)
    tokenizer.backend_tokenizer.no_truncation()
    tokenizer.backend_tokenizer.no_padding()
    tokenizer.backend_tokenizer.save(str(output / 'tokenizer.json'))
    sample = tokenizer(['Pasal 1 memuat ketentuan umum.', 'Izin usaha wajib dipenuhi.'],
                       padding=True, return_tensors='pt')
    module = DenseOrScore(model).eval()
    output_name = 'embedding' if task == 'embed' else 'scores'
    with torch.inference_mode():
        torch.onnx.export(module, (sample['input_ids'], sample['attention_mask']),
            str(output / 'model.onnx'), input_names=['input_ids', 'attention_mask'],
            output_names=[output_name], opset_version=17, dynamo=False,
            dynamic_axes={'input_ids': {0:'batch',1:'tokens'}, 'attention_mask': {0:'batch',1:'tokens'},
                          output_name: {0:'batch'}}, external_data=True)
    # Consolidate all external tensors into one named sidecar for bounded native verification.
    graph = onnx.load(str(output / 'model.onnx'))
    for prop in (('regulagraph.pad_token_id', str(tokenizer.pad_token_id)),
                 ('regulagraph.task', task)):
        entry = graph.metadata_props.add(); entry.key, entry.value = prop
    # Save an external sidecar for FP32, keeping the graph below protobuf's 2 GiB limit.
    if precision == 'fp32':
        onnx.save_model(graph, str(output / 'model.onnx'), save_as_external_data=True,
                        all_tensors_to_one_file=True, location='model.onnx.data', size_threshold=1024)
    else:
        onnx.save_model(graph, str(output / 'model.onnx'))
    onnx.checker.check_model(str(output / 'model.onnx'))
    weights = {name: sha256(output / name) for name in ('model.onnx', 'model.onnx.data')
               if (output / name).exists()}
    (output / 'weights.json').write_text(json.dumps(weights, sort_keys=True, separators=(',', ':')), encoding='utf-8')
    manifest = wire.ModelManifest(model_id=model_id, version=revision, task=wire.MODEL_TASK_EMBED if task=='embed' else wire.MODEL_TASK_RERANK,
        pooling='cls' if task=='embed' else 'logit', normalization='l2' if task=='embed' else 'none',
        max_tokens=max_tokens, precision=precision, backend=f'onnxruntime:1.22.0:{provider}')
    manifest.weights_hash.sha256=sha256(output/'weights.json')
    manifest.tokenizer_hash.sha256=sha256(output/'tokenizer.json')
    if task=='embed': manifest.dimensions=model.config.hidden_size
    validate(manifest)
    manifest_path=output/'model.pbjson'
    manifest_path.write_text(json.dumps(MessageToDict(manifest, preserving_proto_field_name=True),
                                        sort_keys=True, indent=2), encoding='utf-8')
    if source_inventory(source, revision, local_fixture) != source_hashes:
        raise ValueError('source changed during export')
    provenance = {'model_id':model_id, 'revision':revision, 'source_hashes':source_hashes,
                  'source_origin':'SYNTHETIC_FIXTURE' if local_fixture else 'HF_REVISION_RECEIPTS',
                  'manifest_sha256':sha256(manifest_path), 'opset':17,
                  'toolchain':{p:importlib.metadata.version(p) for p in ('torch','transformers','tokenizers','onnx')},
                  'status':'EXPORTED_UNMEASURED', 'reference_required':True}
    (output/'export.json').write_text(json.dumps(provenance,indent=2,sort_keys=True), encoding='utf-8')
    return provenance


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source',type=Path,required=True)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--model-id',required=True)
    parser.add_argument('--revision',required=True)
    parser.add_argument('--task',choices=('embed','rerank'),required=True)
    parser.add_argument('--precision',choices=('fp32','fp16'),default='fp32')
    parser.add_argument('--provider',choices=('cpu','cuda'),default='cpu')
    parser.add_argument('--max-tokens',type=int,default=8192)
    parser.add_argument('--local-fixture',action='store_true',help='Only for fixture: identities, never upstream model evidence')
    args=parser.parse_args()
    print(json.dumps(export_model(**vars(args)),indent=2))


if __name__=='__main__': main()

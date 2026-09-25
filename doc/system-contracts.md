# Spesifikasi kontrak sistem

Dokumen ini merancang kontrak seluruh domain, API, RPC, dan artefak RegulaGraph-ID. Perannya memberi acuan field, pemilik data, invariants, dan failure behavior sebelum implementasi. Ini spesifikasi semantik; schema yang dapat dikompilasi, validator dan fixture C01 kini tersedia. Lihat [implementasi C01](contracts-implementation.md) untuk pemetaan, baseline, dependency dan batas validasi; layanan runtime belum aktif.

Kontrak mencakup keseluruhan produk pada [system-design.md](system-design.md), bukan hanya satu alur demonstrasi. Tipe domain internal tidak semuanya harus menjadi RPC. Semua record persistensi memiliki schema_version dan identitas corpus bila scoped; semua operasi lintas proses membawa correlation, deadline, serta manifest yang relevan. Field di tabel adalah kewajiban semantik; tag Protobuf diputuskan dan dibekukan saat C01, tidak menggunakan posisi kolom sebagai nomor field.

## 1. Aturan umum

| Konsep | Representasi yang dirancang | Validasi |
| --- | --- | --- |
| ID opaque | String ASCII bertipe melalui nama field; entity identity berasal registry, derived ID berasal fingerprint | Tidak disusun dari display name; type/corpus diperiksa di boundary |
| Hash | SHA-256 bytes dengan algoritma dan encoding yang eksplisit | Hash bytes asli berbeda dari hash teks normalisasi |
| Timestamp | UTC timestamp untuk observasi, pemrosesan, event | Tidak digunakan sebagai tanggal hukum tanpa bukti |
| Tanggal hukum | CalendarDate(year, month, day), optional; status unknown/conflict terpisah | Tidak memaksakan timezone ke tanggal tanpa jam |
| Visibility | from_seq inclusive, to_seq exclusive atau unset | Seq adalah knowledge snapshot, bukan tanggal berlaku |
| TextSpan | text_artifact_id, start_byte, end_byte | UTF-8 byte, 0 <= start <= end <= ukuran; batas karakter valid |
| PageLocator | source_id, page_number 1-based, optional normalized bounding box, table/cell path | Posisi mengacu ke artefak dengan hash tertentu |
| Confidence | Optional float [0,1] + method/model ID | Bukan probabilitas terkalibrasi kecuali metode membuktikannya |
| Optional | Field presence eksplisit; zero tidak berarti unknown | Bedakan kosong, tidak berlaku, dan belum diketahui |
| Error | code, safe_message, stage, retryable, item_id?, details terstruktur | Tidak menampilkan credential, stack internal, atau isi sensitif |

Enum wire selalu memiliki UNSPECIFIED = 0 yang ditolak validator pada field wajib. Field yang dihapus di-reserve nomor dan namanya; nomor field tidak digunakan ulang. Penambahan optional tidak otomatis membuat perubahan semantik kompatibel. Unknown field dipertahankan oleh jalur binary yang meneruskannya; bridge JSON diuji tersendiri. Aturan evolusi ini mengikuti [panduan Protobuf](https://protobuf.dev/programming-guides/proto3/).

RPC memakai payload batch atau referensi artefak yang berisi hash, ukuran, media type, dan storage key; tidak mengirim seluruh corpus per request. Batas jumlah item, total bytes, tokens, dan deadline divalidasi kedua sisi berdasarkan konfigurasi versioned. Angka batas runtime dipilih lewat pengukuran dan tidak mengganti workload benchmark. Payload berukuran besar memakai artefak immutable dan manifest, bukan JSON string bebas.

## 2. Common: identitas, context, manifest

| Record | Field semantik | Pemilik dan aturan |
| --- | --- | --- |
| RequestContext | request_id, trace_id, corpus_id, snapshot_ref?, deadline, config_fingerprint, auth_scope_ref | Go membuat context; service memvalidasi scope, tidak mempercayai scope dari input pengguna |
| SnapshotRef | corpus_id, snapshot_id, sequence, manifest_hash, representation_generation | Resolve satu kali; semua backend mengikuti referensi yang sama |
| ArtifactRef | artifact_id, content_hash, storage_key, media_type, byte_size, schema_version | Storage key bukan URL arbitrer; checksum diverifikasi sebelum konsumsi |
| ProducerManifest | software/build, schema, parser/chunker, model/prompt, config hashes, input hashes | Menentukan dependency fingerprint, bukan hanya nama model |
| ModelManifest | model_id/version, weights/tokenizer hashes, task, pooling, normalization, dimensions, max_tokens, precision, backend, prompt_hash? | Query dan index harus memakai representation yang kompatibel |
| TemporalScope | effective_at?, knowledge_snapshot, mode CURRENT/AS_OF/COMPARE, unresolved_policy | Server mengisi tanggal default secara eksplisit; mode compare memiliki daftar tanggal |
| ValidationIssue | code, severity, record_id, field_path, evidence_refs, disposition | Karantina menyimpan alasan; warning tidak menyamarkan error invariant |

## 3. Documents: sumber, struktur, ketentuan, chunk

| Record | Field semantik | Pemilik dan aturan |
| --- | --- | --- |
| SourceObservation | observation_id, portal_id, detail_url, resolved_url, fetched_at, status, etag?, last_modified?, metadata_hash, source_blob_id? | Go; hasil unduhan gagal tidak menjadi dokumen lengkap |
| SourceBlob | source_blob_id, raw_sha256, media_type, byte_size, artifact_ref | Bytes identik boleh berbagi blob; observation/provenance tetap terpisah |
| Regulation | regulation_id, kind, issuer_id, jurisdiction, official_number, year, title, identity_status | Registry Go; nomor/tahun tanpa issuer/type belum cukup untuk merge |
| DocumentEdition | edition_id, regulation_id?, source_refs, language, document_kind, publication_date?, metadata_assertions | Satu regulasi dapat punya file/mirror/edisi berbeda; FAQ/rancangan dibedakan |
| TextArtifact | text_artifact_id, source_blob_id, parser_manifest, normalizer_manifest?, raw_text_ref, normalized_text_ref, mapping_ref, page_results | Rust; manifest normalizer baru wajib untuk STRUCTURE, sedangkan optional presence menjaga decode artefak lama; mapping dapat many-to-many dan tidak mengasumsikan offset linear |
| StructureNode | node_id, kind, label, parent_id?, ordered_children, source_spans, page_locators, table_layout? | Node table/cell menjaga reading order, row/column spans, dan provenance |
| Provision | provision_id, regulation_id, structural_path, parent_provision_id?, lineage_refs | Identitas ketentuan; renumbering memerlukan relasi lineage yang bersumber |
| ProvisionVersion | provision_version_id, provision_id, text_ref/spans, legal_interval, legal_status, supporting_events, reconstruction_manifest?, review_state | Legal interval start inclusive/end exclusive bila diketahui; unknown/conflict tidak dianggap terbuka selamanya |
| LegalChangeEvent | event_id, amending_regulation/version, affected_provisions, operation, replacement_spans, effective_date?, supports, review_state | AMEND/INSERT/REPEAL/RENUMBER; operation pada sebagian ayat tidak menghapus seluruh pasal |
| Chunk | chunk_id, provision_version_refs, text_span, structure_node_refs, parent_refs, exception_refs, chunker_manifest, token_counts | Pemotongan tidak mengubah sumber; chunk lintas node harus menyebut semua versi |
| DocumentBatch | batch_id, sources, text_artifacts, structures, provisions, versions, chunks, issues, dependency_manifest | Rust menghasilkan, Go memvalidasi/persist; partial pages memengaruhi completion status |

Tanggal berlaku/legal status adalah assertion bersumber dan berversi, bukan nilai tunggal portal yang selalu dianggap benar. Tanggal diundangkan, ditetapkan, mulai berlaku, diamati, dan diproses disimpan terpisah. ProvisionVersion dapat mereferensikan teks asli atau rekonstruksi; rekonstruksi harus menyimpan change-event yang dipakai agar dapat diaudit.

## 4. Graph: mention, canonical, assertion, support

| Record | Field semantik | Pemilik dan aturan |
| --- | --- | --- |
| Mention | mention_id, text_span, source/version refs, surface_form, candidate_type, extraction_manifest | Rust; mention tetap ada meskipun resolution berubah |
| CanonicalEntity | canonical_id, entity_type, identity_keys, preferred_label, scope, registry_revision, review_state | ID dikeluarkan registry Go; worker mengusulkan, tidak menciptakan kebenaran global terpisah |
| Alias | alias_id, canonical_id, surface, normalized_lookup, language, scope, valid_interval?, support_refs | Surface identik dapat menunjuk banyak entitas; lookup menghasilkan kandidat |
| ResolutionProposal | proposal_id, mention_ids, candidate_ids, action, evidence, method, confidence?, expected_registry_revision | Rust; optimistic check saat commit, konflik revision dihitung ulang |
| ResolutionDecision | decision_id, proposal_id, assigned_canonical_ids, action, reason, actor, supersedes?, visibility | Go mencatat keputusan otomatis/reviewer dengan riwayat merge/split |
| RelationAssertion | assertion_id, subject_id, predicate_id, object_id, qualifiers, exception_refs, temporal_scope, explicit_or_inferred, ontology_version | Qualifiers dan arah bagian identitas semantik; inferred tidak diubah menjadi explicit; referensi entity qualifier memakai mention_id provisional saat EXTRACT dan canonical_id hanya sesudah RESOLVE |
| SupportRecord | support_id, assertion_id, evidence_spans, source/provision_version refs, extraction_manifest, independent_source_group, review_state | Satu assertion banyak support; mirror tidak dihitung sebagai sumber independen |
| ExtractionBatch | source_document_batch, mentions, assertions, supports, issues, dependencies, completeness/counts, ontology/model/prompt identity, token usage/durations | Artefak immutable keluaran EXTRACT; endpoint masih boleh memakai mention ID dan belum merupakan registry assignment atau delta terpublikasi |
| RegistryCandidateBatch | source_extraction_batch, registry_revision, candidate entities/aliases, per-mention lookup scopes dengan candidate IDs, key normalisasi, tipe, canonical scope, revision, dependencies, completeness | Snapshot kandidat read-only dari Go sebelum RESOLVE; hasil kosong dan revision dicatat per scope agar dependency negatif akurat. Rust menerima satu batch terpin tanpa RPC registry per mention. Scope ID/key harus diturunkan oleh pembaca registry tepercaya dan dibandingkan dengan hasil query nyata; validasi wire hanya membuktikan konsistensi struktur artefak. |
| ResolutionBatch | source_extraction_batch, proposals, decisions, issues, dependencies, completeness/counts, ontology/model identity, registry_revision, token usage/durations | Artefak immutable keluaran RESOLVE; ASSEMBLE hanya memakai keputusan yang revision-nya masih valid |
| EntityProfile | profile_id, canonical_id, summary, evidence_refs, dependency_fingerprint, model_manifest | Ringkasan membantu retrieval; bukan evidence primer tanpa support |
| GraphDelta | delta_id, base_snapshot, registry_revision, upserts, visibility_closures, support_changes, dependencies, validation_report | Go menolak base stale, orphan, schema/ontology mismatch sebelum publish |
| GraphPath | path_id, ordered_node_ids, ordered_assertion_ids, selected_support_ids, coverage, frontier_exhausted | Setiap edge yang dipakai menjawab memiliki support terlihat pada snapshot |

Ontology EXTRACT bersumber dari `configs/ontology-v1.jsonc`. Go Semantic Gateway, Rust worker, dan Go coordinator harus memuat bytes dengan SHA-256 yang sama. `IngestionRequest.config_manifest.input_hashes` memin ontology saat submit; producer Semantic Gateway memasukkan hash yang sama ke `input_hashes`, dan worker/coordinator menolak mismatch sebelum artefak diterima. `ontology_version` pada batch/assertion tetap identitas semantik; hash bytes memastikan perubahan aturan dengan label versi sama tidak lolos diam-diam. Gate ini memeriksa bentuk relasi, bukan kebenaran hukum dari hasil model.

ResolutionProposal memakai client-local correlation ID untuk objek baru; ResolveBatch registry mengembalikan canonical ID sehingga semua referensi downstream dapat diikat ulang sebelum publikasi. Assignment idempotent berdasarkan proposal key dan registry revision. Alias adalah lookup; daftar sinonim tanpa konteks bukan canonical registry.

Adapter PostgreSQL K01 sekarang mendukung registrasi append-only alias unreviewed untuk canonical ID bertipe ontology v1 yang telah dialokasikan, serta lookup batch pada tipe, scope hukum, dan bentuk normalisasi yang eksplisit. ID tipe lama regulasi/organisasi/pasal tetap stabil; perluasan tipe berikutnya membutuhkan review kompatibilitas registry. Satu lookup mengembalikan seluruh kandidat ambigu sampai batas yang diminta dan revision scope untuk hasil positif maupun kosong. Reader mengunci satu snapshot registry dan memverifikasi payload serta kolom indeks sebelum mengeluarkan kandidat. Planner kandidat memakai policy corpus terpin di request ingest dan handoff tepercaya, dengan scope regulasi sumber hanya pada tipe yang dikonfigurasi; overflow ditolak tanpa truncation. Ini belum membuktikan kebenaran `support_refs`, pilihan nilai scope hukum produksi, historical as-of lookup, atau keputusan merge/split; stage RESOLVE harus memverifikasi referensi artefak EXTRACT dan mengikat revision/scope tersebut sebelum assignment atau publication.

Proposal EXTRACT tidak boleh menulis mention ID ke field `canonical_id`. Endpoint assertion dan qualifier entity tetap menunjuk `mention_id` deterministik sampai keputusan resolution yang revision-bound tersedia. ASSEMBLE menolak graph delta yang masih membawa referensi provisional atau canonical assignment stale.

## 5. Evidence, indeks, dan retrieval

| Record | Field semantik | Pemilik dan aturan |
| --- | --- | --- |
| IndexGeneration | generation_id, dense_manifest, lexical_analyzer/dictionary/stats refs, ontology_version, schema_version | Generation incompatible tidak dicampur saat query |
| IndexRecord | record_id, chunk_id, provision_version_refs, dense_vector?, sparse_indices/values?, filter_metadata, dependencies | Sparse indices unik/terurut, panjang values cocok; dense dimensi cocok |
| RetrievalPlan | query_original, query_normalized, intents, linked_entities, temporal_scope, requested_profile, stage_budgets | Go; perubahan query tersimpan untuk audit |
| Candidate | evidence_key, retriever, raw_score, rank, representation, path_refs, filter_decisions | raw_score tidak dianggap comparable antar retriever |
| Evidence | evidence_id, source/version refs, text, parent_refs, source_spans/locators, snapshot_ref, candidate_provenance, graph_paths, legal_status | Text/ID/locator harus konsisten; hydration tidak boleh mengambil versi latest berbeda |
| EvidenceBundle | bundle_id, items, required_path_sets, missing_dependencies, completeness, retrieval_manifest | COMPLETE/PARTIAL/NONE; timeout menjadi error/completion status terpisah |
| RerankResult | pair_id, score, model_manifest, input_tokens, truncation/window_info | Tidak menambahkan kandidat baru atau mengubah identitas evidence |
| ContextBundle | context_id, ordered_evidence_ids, rendered_blocks, token_count, tokenizer_manifest, omitted_required_refs, completeness | Klaim lengkap tidak boleh dibuat bila required refs terpotong |

Query untuk evaluasi retrieval dapat berhenti pada EvidenceBundle tanpa generation. API tetap memakai implementasi retrieval produksi yang sama. IndexBatch dan commit acknowledgement memuat expected/accepted/rejected counts beserta checksum; HTTP sukses pada satu write belum cukup menjadi publication readiness.

## 6. Answers: permintaan, klaim, citation, streaming

| Record | Field semantik | Aturan |
| --- | --- | --- |
| QuestionRequest | question, corpus_id, temporal_scope?, snapshot_id?, response_mode, requested_profile? | Profil eksperimen dibatasi izin; klien tidak dapat melonggarkan benchmark lewat request |
| Claim | claim_id, answer_text_span, evidence_ids, qualifiers, support_status | Span pada teks final berbeda dari span sumber; cakupan dihitung per klaim substantif |
| Citation | citation_id, claim_ids, evidence_id, provision_version_id, source_url, source_span/page_locator | URL disediakan metadata, bukan URL baru hasil halusinasi model |
| Answer | answer_id, request_id, text, claims, citations, paths, semantic_status, completion_status, missing_evidence, conflicts, snapshot, effective_dates, run_manifest | Semantic COMPLETE/PARTIAL/ABSTAIN/CONFLICT/NEEDS_CLARIFICATION; completion SUCCEEDED/FAILED/CANCELLED |
| AnswerEvent | request_id, stream_sequence, event_kind, payload | META, TEXT_DELTA, CITATION, FINAL, ERROR; tepat satu terminal FINAL atau ERROR |

Hanya FINAL dengan completion SUCCEEDED yang dihitung sebagai respons berhasil; abstention sah dapat menjadi sukses transport tetapi dinilai terpisah terhadap gold answerability. ERROR setelah TEXT_DELTA menandai stream gagal. Client memisahkan provisional text dari Answer final; replay event memakai sequence untuk deduplikasi. Reconnect stream tidak otomatis memulai generation kedua; bila buffer resume tidak tersedia, server mengembalikan status eksplisit.

Contoh bentuk JSON konseptual untuk satu jawaban, bukan output implementasi atau klaim hukum nyata:

```json
{
  "request_id": "req-example",
  "corpus_id": "regulagraph-id",
  "question": "Apa syarat kegiatan X pada tanggal tertentu?",
  "temporal_scope": {
    "effective_at": {"year": 2024, "month": 1, "day": 1},
    "mode": "AS_OF"
  },
  "snapshot_id": "snapshot-example",
  "response_mode": "STREAM"
}
```

## 7. Jobs, update, dan snapshot

| Record | Field semantik | Pemilik dan aturan |
| --- | --- | --- |
| IngestionRequest | corpus_id, source_locators/blob_refs, operation, idempotency_key, config_manifest | Go; key sama + payload berbeda menghasilkan CONFLICT |
| Job | job_id, type, state, stage, base_snapshot, input_fingerprint, attempt, lease/fence, checkpoint_ref, errors | Job intent berbeda dari attempt eksekusi |
| Checkpoint | job_id, stage, completed_batch_keys, artifact_hashes, manifest, fence, terminal_status | Output attempt kedaluwarsa tidak boleh commit; response worker baru wajib mengikat outcome terminal yang sama |
| DependencyManifest | artifact_id, dependency_ids/fingerprints, producer_manifest, lookup_scope_revisions | Lookup dengan hasil kosong juga dependency agar penambahan data dapat menginvalidasinya |
| UpdatePlan | source_changes, affected_closure, reuse_set, recompute_set, review_set, base_snapshot | Closure meliputi registry/profile/versi/indeks dan dependency negatif |
| PublicationManifest | snapshot_ref, parent_ref, backend_generations, expected_counts/hashes, acknowledgements, validation_report | Harus cocok seluruh backend sebelum pointer aktif berubah |
| BackendReceipt | publication_id, backend, generation, operations_checksum, counts, durable_ack, search_ready | Search-ready diverifikasi, bukan disamakan dengan enqueue berhasil |
| ReviewItem | item_id, kind, proposed_values, evidence_refs, status, decision/actor/reason, supersedes | Operator tidak dapat mengubah hasil tanpa audit dan update dependency |

Job state: QUEUED -> RUNNING -> WAITING_REVIEW atau STAGED -> VALIDATING -> PUBLISHING -> SUCCEEDED. Stage memuat ACQUIRE/PARSE/STRUCTURE/EXTRACT/RESOLVE/ASSEMBLE/INDEX. RETRY_WAIT dapat kembali ke stage yang gagal dengan attempt baru. FAILED/CANCELLED adalah terminal job, tetapi cancellation saat PUBLISHING harus menyelesaikan atau mengompensasi publication terlebih dahulu. State snapshot terpisah dijelaskan di [storage-consistency.md](storage-consistency.md).

## 8. Inference dan operasi internal

| Operasi rencana | Host | Input dan output |
| --- | --- | --- |
| Worker.ProcessBatch | Rust | Context + job/lease + immutable source/registry refs -> checkpoint + stage-specific DocumentBatch/ExtractionBatch/ResolutionBatch/GraphDelta/IndexBatch ref |
| Worker.GetStatus / Cancel | Rust | Job/attempt/fence -> progress atau cancellation acknowledgement; Go tetap sumber status persisten |
| Inference.EmbedBatch | C++ | Model manifest + item ID/text + purpose/query-or-document -> vector per item, tokens, truncation, errors |
| Inference.RerankBatch | C++ | Manifest + pair ID/query/text -> skor per pair, tokens, errors |
| Inference.GetCapabilities | C++ | -> manifest model termuat, supported tasks, limits, readiness |
| Semantic.ExtractBatch | Go gateway | Typed text/evidence + ontology/schema + model/prompt -> structured proposals dengan source spans |
| Semantic.ResolveBatch | Go gateway | Ambiguous mention/candidates/evidence -> proposal, bukan mutasi registry |
| Semantic.SummarizeBatch | Go gateway | Entity facts/evidence -> profile draft dan support refs |
| Registry.ResolveBatch | Go coordinator | Identity keys/proposals + expected revision -> stable ID assignments/decisions atau conflict |

Operasi semantik dipisah dari Registry.ResolveBatch: model memberi rekomendasi, registry memiliki assignment. Coordinator dapat mengirim registry artefact read-only pada job untuk menghindari round trip lookup. Callback gateway tidak boleh menggunakan pool yang seluruh slotnya sedang menunggu worker tersebut; pisahkan admission dan connection pool agar tidak terjadi deadlock siklik.

Semua batch mempunyai item ID unik dan hasil one-to-one atau error eksplisit; urutan respons bukan satu-satunya mekanisme korelasi. Pembatalan membebaskan queue slot dan menghentikan kerja yang belum dimulai. Batch GPU yang sudah berjalan dapat selesai tetapi hasil tidak dipublikasikan setelah lease/fence kedaluwarsa. Retry hanya untuk operasi idempotent atau operation key persisten.

## 9. API publik dan operasional yang dirancang

| Method/path | Fungsi | Hak akses dan hasil |
| --- | --- | --- |
| POST /v1/questions | Answer biasa atau stream sesuai response_mode | query; snapshot/effective date eksplisit pada respons |
| POST /v1/evidence/search | Retrieval tanpa generation | query/evaluate sesuai profil; EvidenceBundle |
| POST /v1/ingestions | Daftar sumber untuk ingest/update | operator; 202 + job ID; idempotency key wajib |
| GET /v1/jobs/{id} | Stage, attempt, errors, checkpoint summary | operator corpus terkait |
| POST /v1/jobs/{id}/cancel | Permintaan pembatalan | operator; asynchronous, tidak menjanjikan rollback instant |
| GET /v1/documents/{id} | Metadata, source observations, edition refs | query; corpus/snapshot diperiksa |
| GET /v1/provisions/{id}/versions | Versi dan change events beserta provenance | query; tidak merangkum unknown menjadi berlaku |
| GET /v1/snapshots/{id} | Manifest dan status snapshot | query/operator sesuai detail |
| GET /v1/reviews | Item unresolved/karantina | operator, pagination dan filter corpus |
| POST /v1/reviews/{id}/decisions | Keputusan bersumber dengan expected revision | reviewer; conflict bila revision berubah |
| GET /health/live | Proses hidup | Tidak memanggil semua backend setiap probe |
| GET /health/ready | Siap untuk capability query/ingest yang dikonfigurasi | Gagal bila dependency wajib tidak siap |

Daftar/list endpoint memakai opaque cursor terikat filter, corpus, dan snapshot; pagination tidak berpindah ke snapshot terbaru diam-diam. Respons error HTTP menggunakan record error yang sama secara semantik; status 400 invalid, 401 unauthenticated, 403 forbidden, 404 not found, 409 conflict, 413 terlalu besar, 429 kapasitas/rate, 503 dependency unavailable, 504 deadline. Error bisnis tidak disembunyikan dalam 200 sukses.

## 10. Evaluasi, artifacts, dan kompatibilitas

| Artefak evaluasi | Isi wajib |
| --- | --- |
| DatasetManifest | dataset/version/hash, corpus snapshot, base_question_group, split, label schema, review provenance |
| GoldQuestion | pertanyaan, slice labels, tanggal, answerability, expected claims, acceptable evidence sets, required paths |
| RunManifest | software/config/model/prompt/dataset/corpus/target-suite hashes, hardware/limits, cache, workload, seed |
| Observation | scheduled arrival, outcome, stage durations, first substantive token, completion, tokens, errors, quality judgments |
| GateResult | gate ID, applicable workload, denominator, estimate, uncertainty, threshold reference, status/reason, raw artifact refs |

Perubahan schema memiliki compatibility matrix old-reader/new-writer dan sebaliknya, fixture round trip Go/Rust/C++/Python, unknown/optional/enum tests, Unicode offset, dan oversized payload tests. Kontrak wire dan storage boleh berevolusi berbeda tetapi transformasinya wajib eksplisit. Generated binding bukan sumber schema kedua.

Semua file proto existing tetap pemilik utama: common untuk primitive/context/manifest; documents untuk struktur/versi/chunk; graph untuk canonical/assertion/delta; evidence untuk indeks/retrieval/context; answers untuk query/result/event; jobs untuk job/dependency/publication/review; inference untuk operasi model dan manifest task. Import harus DAG: common di bawah; documents di atas common; graph di atas documents/common; evidence di atas graph/documents/common; answers di atas evidence/common; jobs di atas domain terkait; inference memakai common/documents/graph tetapi tidak mengimpor jobs/answers. Registry RPC dapat ditempatkan pada graph dan worker RPC pada jobs. Manifest evaluasi dimiliki evaluation/datasets, dengan referensi kontrak produksi yang sama.

## Resolusi model kontekstual

`AmbiguousMention.context_items` membawa teks chunk dengan provenance versi chunk, terpisah dari exact mention evidence. Caller memverifikasi bytes artefak; gateway menguji UTF-8, containment dan source correlation. Gateway baru mensyaratkan konteks meskipun field aditif tetap opsional untuk binary compatibility. `ResolutionProposal.rationale` dan `supporting_context_ids` merujuk catatan request immutable, tidak menjadi approval atau probabilitas terkalibrasi. Workflow mengarsipkan input/output dan memverifikasi producer/model serta revision sebelum meneruskan proposal. Lihat [kontrak runtime](semantic-resolution.md); baseline lama tidak ditulis ulang.

`AmbiguousMention.candidate_contexts` mengikat canonical/alias ID ke support mention dan excerpt dokumen kandidat. Katalog checkpoint EXTRACT hanya menyediakan locator; workflow memverifikasi alias/support, hash, corpus, snapshot, auth scope, ontology, document closure, serta exact bytes sebelum sampling. Gateway dan workflow mewajibkan LINK mengutip konteks mention serta support kandidat terpilih. Kandidat boleh tetap ambigu; tidak ada merge atau approval yang tersirat dari rationale. Dependency audit mencakup artefak sumber kandidat dan menolak metadata bertentangan untuk ID sama. Runtime lama yang mengabaikan field aditif tidak menjamin gate ini; upgrade workflow/gateway dan pin prompt bersama.

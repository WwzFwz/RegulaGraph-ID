// Menyediakan alias kompatibilitas untuk record domain yang dipersist adapter PostgreSQL.
// Peran: menjaga API paket storage ringkas sambil memastikan workflow/indexing bergantung
// pada domain, bukan pada implementasi database.
// Kontrak: bentuk utama berada di internal/domain/operations.go; perubahan field harus
// disertai migration serta concurrency test. File ini tidak mendefinisikan vocabulary baru.
// Benchmark: alias tidak menambah allocation; ukur record bersama query pemiliknya.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Status: tipe job, publication reservation, dan snapshot pin S01 aktif.
package postgres

import "regulagraph.local/server/internal/domain"

type JobIntent = domain.JobIntent
type JobRecord = domain.JobRecord
type PublicationReservation = domain.PublicationReservation
type SnapshotPin = domain.SnapshotPin

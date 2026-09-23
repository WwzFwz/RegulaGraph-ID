// Penjadwalan bounded batching untuk request inference.
//
// Peran dalam komponen:
// Header publik wrapper inference native.
//
// Integrasi dan perhatian performa:
// Ukur waktu menunggu batch, throughput, dan p99; jangan menunggu batch penuh tanpa deadline. Prioritas online/offline harus terlihat.
//
// Benchmark dan gate penerimaan:
// Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//
// Status: scheduler bounded berbasis event loop aktif; backend model dan transport belum aktif.
// Rekomendasi implementasi berikutnya:
// Hubungkan scheduler ke session/model warm dan telemetry antrean tanpa memuat model per request.
// Bukti verifikasi: Test overload, fairness, starvation and cancelled items; measure p95/p99 queue wait, utilization and RSS/VRAM; header inclusion must not allocate model resources.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

#pragma once

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <list>
#include <map>
#include <optional>
#include <string>
#include <unordered_map>
#include <vector>

namespace regulagraph::inference {

using BatchClock = std::chrono::steady_clock;
using BatchTime = BatchClock::time_point;

enum class WorkClass { query, bulk };
enum class EnqueueStatus { accepted, invalid, duplicate, expired, overloaded };

// The runtime retains request payloads separately; the scheduler owns only bounded metadata.
struct BatchItem {
    std::string item_id;
    WorkClass work_class;
    std::uint32_t estimated_tokens;
    BatchTime deadline;
};

struct SchedulerLimits {
    std::size_t maximum_batch_items;
    std::uint64_t maximum_batch_tokens;
    std::size_t maximum_query_pending;
    std::size_t maximum_bulk_pending;
    std::chrono::milliseconds maximum_wait;
    std::size_t maximum_consecutive_query_batches;
};

struct BatchSelection {
    std::vector<BatchItem> items;
    std::vector<BatchItem> expired;
    std::optional<WorkClass> selected_class;
};

struct Admission {
    EnqueueStatus status;
    std::vector<BatchItem> expired;
};

// One event-loop thread owns this scheduler. The caller invokes Next at NextWakeup or after
// enqueue/cancellation and maps every expired item in Admission/BatchSelection to a typed C01
// error. Admission cleans expired items before checking capacity; its expired list must be handled
// even when the new item is rejected. Allocation failure or internal index corruption is fatal:
// Enqueue/Next are noexcept so the worker process terminates instead of stranding items silently.
// A supervising runtime must restart the worker and clients must retry outstanding requests.
class BatchScheduler {
public:
    explicit BatchScheduler(SchedulerLimits limits);

    Admission Enqueue(BatchItem item, BatchTime now) noexcept;
    bool Cancel(const std::string& item_id);
    BatchSelection Next(BatchTime now) noexcept;
    std::optional<BatchTime> NextWakeup() const;
    std::size_t Pending() const noexcept;

private:
    struct PendingItem {
        BatchItem item;
        BatchTime enqueued_at;
    };

    using Queue = std::list<PendingItem>;
    using Deadlines = std::multimap<BatchTime, std::string>;
    struct Locator {
        WorkClass work_class;
        Queue::iterator queued;
        Deadlines::iterator deadline;
    };

    bool Ready(const Queue& queue, const Deadlines& deadlines, BatchTime now) const;
    void Expire(BatchTime now, std::vector<BatchItem>& expired) noexcept;

    SchedulerLimits limits_;
    Queue query_;
    Queue bulk_;
    Deadlines query_deadlines_;
    Deadlines bulk_deadlines_;
    std::unordered_map<std::string, Locator> pending_;
    std::size_t consecutive_query_batches_ = 0;
};
}

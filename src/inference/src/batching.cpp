// Penjadwalan bounded batching untuk request inference.
//
// Peran dalam komponen:
// Lokasi implementasi untuk header inference terkait.
//
// Integrasi dan perhatian performa:
// Ukur waktu menunggu batch, throughput, dan p99; jangan menunggu batch penuh tanpa deadline. Prioritas online/offline harus terlihat.
//
// Benchmark dan gate penerimaan:
// Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//
// Status: scheduler bounded berbasis event loop aktif; transport/model belum tersambung.
// Rekomendasi implementasi berikutnya:
// Hubungkan NextWakeup, item errors, dan telemetry antrean ke runtime model warm.
// Bukti verifikasi: Test overload, fairness, starvation and cancelled items; measure p95/p99 queue wait, utilization and RSS/VRAM.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

#include "regulagraph/inference/batching.hpp"

#include <algorithm>
#include <exception>
#include <iterator>
#include <stdexcept>
#include <utility>

namespace regulagraph::inference {

namespace {
bool valid_id(const std::string& value) {
    if (value.empty() || value.size() > 256) {
        return false;
    }
    const auto alphanumeric = [](char c) {
        return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9');
    };
    if (!alphanumeric(value.front())) {
        return false;
    }
    return std::all_of(value.begin() + 1, value.end(), [&](char c) {
        return alphanumeric(c) || c == '.' || c == '_' || c == ':' || c == '-';
    });
}
}

BatchScheduler::BatchScheduler(SchedulerLimits limits) : limits_(limits) {
    if (limits.maximum_batch_items == 0 || limits.maximum_batch_tokens == 0 ||
        limits.maximum_query_pending == 0 || limits.maximum_bulk_pending == 0 ||
        limits.maximum_wait.count() <= 0 || limits.maximum_consecutive_query_batches == 0) {
        throw std::invalid_argument("batch scheduler requires positive bounded limits");
    }
}

Admission BatchScheduler::Enqueue(BatchItem item, BatchTime now) noexcept {
    Admission admission{EnqueueStatus::invalid, {}};
    Expire(now, admission.expired);
    if (!valid_id(item.item_id) || item.estimated_tokens == 0 ||
        item.estimated_tokens > limits_.maximum_batch_tokens ||
        (item.work_class != WorkClass::query && item.work_class != WorkClass::bulk)) {
        return admission;
    }
    if (item.deadline <= now) {
        admission.status = EnqueueStatus::expired;
        return admission;
    }
    if (pending_.find(item.item_id) != pending_.end()) {
        admission.status = EnqueueStatus::duplicate;
        return admission;
    }
    auto& queue = item.work_class == WorkClass::query ? query_ : bulk_;
    auto& deadlines = item.work_class == WorkClass::query ? query_deadlines_ : bulk_deadlines_;
    const auto capacity = item.work_class == WorkClass::query ? limits_.maximum_query_pending
                                                          : limits_.maximum_bulk_pending;
    if (queue.size() >= capacity) {
        admission.status = EnqueueStatus::overloaded;
        return admission;
    }
    const auto key = item.item_id;
    const auto deadline = item.deadline;
    const auto work_class = item.work_class;
    // These structures are process-local and must advance together. noexcept
    // makes allocation failure fail-stop; the worker supervisor owns recovery.
    queue.push_back(PendingItem{std::move(item), now});
    const auto deadline_it = deadlines.emplace(deadline, key);
    pending_.emplace(key, Locator{work_class, std::prev(queue.end()), deadline_it});
    admission.status = EnqueueStatus::accepted;
    return admission;
}

bool BatchScheduler::Cancel(const std::string& item_id) {
    const auto found = pending_.find(item_id);
    if (found == pending_.end()) {
        return false;
    }
    auto& queue = found->second.work_class == WorkClass::query ? query_ : bulk_;
    auto& deadlines = found->second.work_class == WorkClass::query ? query_deadlines_ : bulk_deadlines_;
    queue.erase(found->second.queued);
    deadlines.erase(found->second.deadline);
    pending_.erase(found);
    return true;
}

void BatchScheduler::Expire(BatchTime now, std::vector<BatchItem>& expired) noexcept {
    for (const auto work_class : {WorkClass::query, WorkClass::bulk}) {
        auto& queue = work_class == WorkClass::query ? query_ : bulk_;
        auto& deadlines = work_class == WorkClass::query ? query_deadlines_ : bulk_deadlines_;
        while (!deadlines.empty() && deadlines.begin()->first <= now) {
            const auto found = pending_.find(deadlines.begin()->second);
            if (found == pending_.end()) {
                std::terminate();
            }
            expired.push_back(std::move(found->second.queued->item));
            queue.erase(found->second.queued);
            deadlines.erase(found->second.deadline);
            pending_.erase(found);
        }
    }
}

bool BatchScheduler::Ready(const Queue& queue, const Deadlines& deadlines, BatchTime now) const {
    if (queue.empty()) {
        return false;
    }
    if (queue.size() >= limits_.maximum_batch_items ||
        now - queue.front().enqueued_at >= limits_.maximum_wait) {
        return true;
    }
    return !deadlines.empty() && deadlines.begin()->first - now <= limits_.maximum_wait;
}

BatchSelection BatchScheduler::Next(BatchTime now) noexcept {
    BatchSelection selection;
    Expire(now, selection.expired);
    const bool query_ready = Ready(query_, query_deadlines_, now);
    const bool bulk_ready = Ready(bulk_, bulk_deadlines_, now);
    if (!query_ready && !bulk_ready) {
        return selection;
    }
    const bool choose_query = query_ready &&
        (!bulk_ready || consecutive_query_batches_ < limits_.maximum_consecutive_query_batches);
    auto& queue = choose_query ? query_ : bulk_;
    auto& deadlines = choose_query ? query_deadlines_ : bulk_deadlines_;
    selection.selected_class = choose_query ? WorkClass::query : WorkClass::bulk;
    std::uint64_t tokens = 0;
    while (!queue.empty() && selection.items.size() < limits_.maximum_batch_items) {
        if (queue.front().item.estimated_tokens > limits_.maximum_batch_tokens - tokens) {
            break;
        }
        tokens += queue.front().item.estimated_tokens;
        const auto found = pending_.find(queue.front().item.item_id);
        if (found == pending_.end()) {
            std::terminate();
        }
        selection.items.push_back(std::move(queue.front().item));
        deadlines.erase(found->second.deadline);
        pending_.erase(found);
        queue.pop_front();
    }
    if (choose_query) {
        if (consecutive_query_batches_ < limits_.maximum_consecutive_query_batches) {
            ++consecutive_query_batches_;
        }
    } else {
        consecutive_query_batches_ = 0;
    }
    return selection;
}

std::optional<BatchTime> BatchScheduler::NextWakeup() const {
    std::optional<BatchTime> wakeup;
    const auto consider = [&](BatchTime time) {
        if (!wakeup || time < *wakeup) {
            wakeup = time;
        }
    };
    for (const auto work_class : {WorkClass::query, WorkClass::bulk}) {
        const auto& queue = work_class == WorkClass::query ? query_ : bulk_;
        const auto& deadlines = work_class == WorkClass::query ? query_deadlines_ : bulk_deadlines_;
        if (!queue.empty()) {
            if (queue.size() >= limits_.maximum_batch_items) {
                consider(queue.front().enqueued_at);
            }
            consider(queue.front().enqueued_at + limits_.maximum_wait);
        }
        if (!deadlines.empty()) {
            consider(deadlines.begin()->first - limits_.maximum_wait);
        }
    }
    return wakeup;
}

std::size_t BatchScheduler::Pending() const noexcept {
    return pending_.size();
}
}

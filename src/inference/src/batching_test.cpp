// Exercises bounded native scheduling without a model runtime: deadlines, cancellation,
// overload, token limits, and fairness. These deterministic tests do not prove model parity,
// queue p95/p99, or throughput targets in configs/benchmark-targets.yaml.
#include "regulagraph/inference/batching.hpp"

#include <chrono>
#include <stdexcept>
#include <string>
#include <utility>

using regulagraph::inference::BatchItem;
using regulagraph::inference::BatchScheduler;
using regulagraph::inference::BatchTime;
using regulagraph::inference::EnqueueStatus;
using regulagraph::inference::SchedulerLimits;
using regulagraph::inference::WorkClass;

namespace {
void require(bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

BatchItem item(const char* id, WorkClass lane, std::uint32_t tokens, BatchTime deadline) {
    return BatchItem{std::string(id), lane, tokens, deadline};
}

EnqueueStatus admit(BatchScheduler& scheduler, BatchItem value, BatchTime now) {
    const auto result = scheduler.Enqueue(std::move(value), now);
    require(result.expired.empty(), "unexpected expired item during admission");
    return result.status;
}
}

int main() {
    using namespace std::chrono_literals;
    const auto start = BatchTime{} + 1s;
    const auto limits = SchedulerLimits{2, 8, 3, 3, 10ms, 2};
    BatchScheduler scheduler(limits);
    require(admit(scheduler, item("q:one", WorkClass::query, 3, start + 1s), start) ==
                EnqueueStatus::accepted, "first query refused");
    require(scheduler.Next(start).items.empty(), "underfilled batch ran before wait");
    require(scheduler.NextWakeup() == start + 10ms, "wakeup missed maximum wait");
    require(admit(scheduler, item("q:two", WorkClass::query, 3, start + 1s), start) ==
                EnqueueStatus::accepted, "second query refused");
    auto selected = scheduler.Next(start);
    require(selected.items.size() == 2 && selected.items[0].item_id == "q:one" &&
                selected.items[1].item_id == "q:two", "query batch order changed");
    require(scheduler.Pending() == 0, "dispatched items remained queued");

    BatchScheduler limited(limits);
    for (int index = 0; index < 3; ++index) {
        require(admit(limited, item(("bulk:" + std::to_string(index)).c_str(), WorkClass::bulk,
                                     5, start + 1s), start) == EnqueueStatus::accepted,
                "bulk capacity refused early");
    }
    require(admit(limited, item("bulk:over", WorkClass::bulk, 1, start + 1s), start) ==
                EnqueueStatus::overloaded, "bulk overload not reported");
    require(limited.Cancel("bulk:1"), "queued item could not be cancelled");
    require(!limited.Cancel("bulk:1"), "cancelled item was still queued");
    require(admit(limited, item("bulk:0", WorkClass::query, 1, start + 1s), start) ==
                EnqueueStatus::duplicate, "duplicate ID across lanes accepted");
    require(admit(limited, item("expired", WorkClass::query, 1, start), start) ==
                EnqueueStatus::expired, "expired request accepted");
    require(admit(limited, item("too-large", WorkClass::query, 9, start + 1s), start) ==
                EnqueueStatus::invalid, "unbatchable request accepted");
    selected = limited.Next(start + 10ms);
    require(selected.items.size() == 1 && selected.items[0].item_id == "bulk:0" &&
                limited.Pending() == 1, "token budget or FIFO violated");
    selected = limited.Next(start + 10ms);
    require(selected.items.size() == 1 && selected.items[0].item_id == "bulk:2",
            "second token-limited batch was lost");

    BatchScheduler partial(SchedulerLimits{2, 8, 3, 3, 10ms, 2});
    for (int index = 0; index < 3; ++index) {
        require(admit(partial, item(("partial:" + std::to_string(index)).c_str(),
                                     WorkClass::query, 5, start + 1s), start) ==
                    EnqueueStatus::accepted, "partial batch request refused");
    }
    require(partial.Next(start).items.size() == 1, "first token-limited batch wrong");
    require(partial.NextWakeup() == start,
            "remaining full batch was delayed until maximum wait");
    require(partial.Next(start).items.size() == 1, "second token-limited batch missing");

    BatchScheduler fair(SchedulerLimits{1, 8, 3, 3, 10ms, 2});
    for (int index = 0; index < 3; ++index) {
        require(admit(fair, item(("query:" + std::to_string(index)).c_str(), WorkClass::query,
                                  1, start + 1s), start) == EnqueueStatus::accepted,
                "fairness query refused");
    }
    require(admit(fair, item("bulk:fair", WorkClass::bulk, 1, start + 1s), start) ==
                EnqueueStatus::accepted, "fairness bulk refused");
    require(fair.Next(start).selected_class == WorkClass::query, "query lost initial priority");
    require(fair.Next(start).selected_class == WorkClass::query, "query lost allowed second turn");
    selected = fair.Next(start);
    require(selected.selected_class == WorkClass::bulk && selected.items.size() == 1,
            "bulk starved behind query queue");

    BatchScheduler deadline(limits);
    require(admit(deadline, item("q:expires", WorkClass::query, 1, start + 2ms), start) ==
                EnqueueStatus::accepted, "short deadline refused");
    selected = deadline.Next(start + 2ms);
    require(selected.items.empty() && selected.expired.size() == 1 &&
                selected.expired[0].item_id == "q:expires", "expired item was dispatched");

    BatchScheduler admission(SchedulerLimits{2, 8, 1, 1, 10ms, 2});
    require(admit(admission, item("old", WorkClass::query, 1, start + 1ms), start) ==
                EnqueueStatus::accepted, "old request refused");
    const auto replacement = admission.Enqueue(item("new", WorkClass::query, 1, start + 1s),
                                               start + 2ms);
    require(replacement.status == EnqueueStatus::accepted && replacement.expired.size() == 1 &&
                replacement.expired[0].item_id == "old", "expired slot blocked new request");
}

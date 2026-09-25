// Regression for cancellation overlapping a backend fault at the native boundary.
// Uses the production classifier: an OOM, wrapped message, or unrelated ORT status
// must remain a failure even when the caller cancels. No model quality/latency claim.
#include "runtime_errors.hpp"
#include <iostream>
#include <string>

int main() {
    using regulagraph::inference::ConfirmedTermination;
    const std::string termination="Exiting due to terminate flag being set to true.";
    const bool pass=ConfirmedTermination(ORT_FAIL,termination,true) &&
        !ConfirmedTermination(ORT_FAIL,termination,false) &&
        !ConfirmedTermination(ORT_RUNTIME_EXCEPTION,termination,true) &&
        !ConfirmedTermination(ORT_OK,termination,true) &&
        !ConfirmedTermination(ORT_FAIL,"CUDA failure 2: out of memory",true) &&
        !ConfirmedTermination(ORT_FAIL,"kernel failed: "+termination,true) &&
        !ConfirmedTermination(ORT_FAIL,termination+" CUDA failure",true) &&
        !ConfirmedTermination(ORT_FAIL,"",true);
    if(!pass) {std::cerr<<"execution failure incorrectly classified as cancellation\n";return 1;}
    return 0;
}

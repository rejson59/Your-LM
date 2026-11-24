#include "ggml.h"
#include "ggml-backend.h"
#include "ggml-rpc.h"

#include <cassert>
#include <cstdint>
#include <cstdio>
#include <vector>

int main(int argc, char ** argv) {
    assert(argc == 2);
    ggml_backend_t backend = ggml_backend_rpc_init(argv[1], 0);
    assert(backend != nullptr);

    size_t memory_free = 0;
    size_t memory_total = 0;
    ggml_backend_dev_memory(ggml_backend_get_device(backend), &memory_free, &memory_total);
    assert(memory_free > 0);
    assert(memory_free < memory_total); // Linux/Android must report MemAvailable, not all physical RAM.

    ggml_init_params params = {
        /* .mem_size   = */ ggml_tensor_overhead(),
        /* .mem_buffer = */ nullptr,
        /* .no_alloc   = */ true,
    };
    ggml_context * ctx = ggml_init(params);
    assert(ctx != nullptr);

    const size_t size = 12*1024*1024; // above llama.cpp's cache threshold
    ggml_tensor * tensor = ggml_new_tensor_1d(ctx, GGML_TYPE_I8, size);
    ggml_backend_buffer_t buffer = ggml_backend_alloc_ctx_tensors(ctx, backend);
    assert(buffer != nullptr);
    ggml_backend_buffer_set_usage(buffer, GGML_BACKEND_BUFFER_USAGE_WEIGHTS);

    std::vector<uint8_t> expected(size);
    for (size_t i = 0; i < expected.size(); ++i) {
        expected[i] = (uint8_t) ((i*131 + 17) & 0xff);
    }
    ggml_backend_tensor_set(tensor, expected.data(), 0, expected.size());

    std::vector<uint8_t> actual(size);
    ggml_backend_tensor_get(tensor, actual.data(), 0, actual.size());
    assert(actual == expected);

    ggml_backend_buffer_free(buffer);
    ggml_free(ctx);
    ggml_backend_free(backend);
    std::puts("RPC cache tensor round-trip passed");
    return 0;
}

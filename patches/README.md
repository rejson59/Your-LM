# Pinned llama.cpp patches

`llama-rpc-streaming-cache.patch` applies only to llama.cpp commit
`c0b97be0b297177a88df08e96c01420976bb18d0` (`b12562`). Build scripts call
`scripts/apply-llama-patches.sh`, which fails closed if the patch no longer
matches that source.

The patch replaces full-tensor temporary allocations in the RPC file cache
with a shared 16 MiB streaming buffer. Cache files are published by rename only
after a complete write; cache hits are streamed and checked for exact size and
FNV-1a hash. A cache write failure no longer aborts an otherwise valid tensor
transfer.

Run the functional gate with:

```sh
LLAMA_SRC=/path/to/pinned/llama.cpp tests/rpc-cache-smoke.sh
```

It exercises a cache miss and hit, then verifies automatic recovery from both
a truncated file and a same-sized file with corrupted contents.

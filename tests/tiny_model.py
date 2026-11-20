"""Write a synthetic 4-block GGUF for plumbing tests, NOT a trained model."""
import math
import struct
import sys
from pathlib import Path


def pack(fmt, *xs):
    return struct.pack('<' + fmt, *xs)


def string(s):
    s = s.encode()
    return pack('Q', len(s)) + s


tokens = ['<unk>', '<s>', '</s>', '▁'] + [f'<0x{i:02X}>' for i in range(256)]
meta = []

def add(key, kind, val):
    meta.append(string(key) + pack('I', kind) + val)

add('general.architecture', 8, string('llama'))
for field, n in [('block_count', 4), ('embedding_length', 64), ('context_length', 2048),
                 ('feed_forward_length', 128), ('attention.head_count', 4),
                 ('attention.head_count_kv', 4), ('rope.dimension_count', 16)]:
    add('llama.' + field, 4, pack('I', n))
add('llama.attention.layer_norm_rms_epsilon', 6, pack('f', 1e-5))
add('tokenizer.ggml.model', 8, string('llama'))
add('tokenizer.ggml.tokens', 9, pack('IQ', 8, len(tokens)) + b''.join(map(string, tokens)))
add('tokenizer.ggml.scores', 9, pack('IQ', 6, len(tokens)) + pack('f', 0) * len(tokens))
add('tokenizer.ggml.token_type', 9, pack('IQ', 5, len(tokens)) + pack('4i', 2, 3, 3, 1) + pack('i', 6) * 256)
header = []
data = bytearray()

def tensor(name, *shape):
    header.append(string(name) + pack('I', len(shape)) + pack('Q' * len(shape), *shape) + pack('IQ', 0, len(data)))
    data.extend(pack('f', 0.01) * math.prod(shape))
    data.extend(b'\0' * (-len(data) % 32))

tensor('token_embd.weight', 64, len(tokens))
tensor('output_norm.weight', 64)
for i in range(4):
    for k, shape in [('attn_norm', (64,)), ('ffn_norm', (64,)), ('attn_q', (64, 64)),
                     ('attn_k', (64, 64)), ('attn_v', (64, 64)), ('attn_output', (64, 64)),
                     ('ffn_gate', (64, 128)), ('ffn_up', (64, 128)), ('ffn_down', (128, 64))]:
        tensor(f'blk.{i}.{k}.weight', *shape)
head = b'GGUF' + pack('IQQ', 3, len(header), len(meta)) + b''.join(meta + header)
Path(sys.argv[1]).write_bytes(head + b'\0' * (-len(head) % 32) + data)

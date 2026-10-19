package setup

// ModelClass is what a service's model is, read by the host (isannd) from the
// weight files of the model running now: format, name, architecture,
// quantization and size. It rides on ServiceInfo.ModelClass into the register
// frame and out of RV's /v1/nodes.
//
// 🔴 A PEER CANNOT DERIVE THIS FROM Model. Model is whatever the file was
// called - the file shipped as "Qwen2.5-1.5B" counts 1.78B parameters, since
// the 1.5B leaves the embeddings out - and only the host can open the file.
// It is still the node's own word: nothing here is signed or checked by a
// prober, so it is for showing and choosing, not for paying.
//
// This file is copied as-is into GLink, servers and mesh (pkg/setup).
type ModelClass struct {
	// Format is the weight file format: gguf | safetensors, or ckpt | bin |
	// pt | pth, whose headers cannot be read without running pickle code -
	// for those Format is the only field set.
	Format string `json:"format"`
	// Name is the name written inside the weight file by whoever made it: GGUF
	// general.name, or a safetensors file's __metadata__ "modelspec.title"
	// (most safetensors files carry none). Free text the maker chose - it can
	// be anything, as a file name can - so it is shown, never trusted. Control
	// characters are removed and it is cut to 128 bytes. Empty when the file
	// names nothing.
	Name string `json:"name,omitempty"`
	// Arch is the architecture: qwen2 | llama | gemma3 ... (GGUF metadata,
	// HF config.json model_type) or sd15 | sdxl | sd3 | flux (package.json,
	// else told apart by tensor names). Empty when unknown.
	Arch string `json:"arch,omitempty"`
	// Quantized is whether most weights are stored below 16 bits (GGUF Q2-Q8,
	// FP8, INT8, GPTQ, AWQ, bitsandbytes). nil = unknown.
	Quantized *bool `json:"quantized,omitempty"`
	// Quant names the storage: Q4_K_M | Q8_0 | F16 | BF16 | F32 | FP8 |
	// GPTQ-4bit | AWQ-4bit ...
	Quant string `json:"quant,omitempty"`
	// Bits is the average bits per weight: tensor data bytes x 8 / ParamsTotal.
	Bits float64 `json:"bits,omitempty"`
	// Core is the part ParamsCore counts when it is not the whole file: unet
	// (SD 1.x / 2.x / XL) or transformer (SD3, Flux). Empty = the whole file.
	Core string `json:"core,omitempty"`
	// ParamsCore is the size that classes the model: the part that does the
	// work (an SD file also carries its text encoder and VAE).
	ParamsCore int64 `json:"params_core,omitempty"`
	// ParamsTotal counts every weight in the file(s), embeddings included.
	ParamsTotal int64 `json:"params_total,omitempty"`

	// What follows is the shape of a transformer, read from the same headers
	// (GGUF "<arch>.<key>", HF config.json). Each is 0 = the file does not say
	// (an image checkpoint has none of them, and a converter may leave a key
	// out), never "zero layers".
	//
	// Layers is the number of transformer blocks (GGUF block_count, HF
	// num_hidden_layers).
	Layers int `json:"layers,omitempty"`
	// ContextMax is the longest context the model was trained for (GGUF
	// context_length, HF max_position_embeddings). The context a node runs it
	// with is a setting and rides in ServiceInfo.Inspect, not here.
	ContextMax int `json:"context_max,omitempty"`
	// Hidden is the width of a layer (GGUF embedding_length, HF hidden_size).
	Hidden int `json:"hidden,omitempty"`
	// Heads and KVHeads are the attention heads and the heads that keep keys and
	// values (GGUF attention.head_count / head_count_kv, HF num_attention_heads /
	// num_key_value_heads). KVHeads sets how big the KV cache is.
	Heads   int `json:"heads,omitempty"`
	KVHeads int `json:"kv_heads,omitempty"`
	// Experts is how many experts a mixture-of-experts model has and ExpertsUsed
	// how many it runs per token; both 0 for a dense model.
	Experts     int `json:"experts,omitempty"`
	ExpertsUsed int `json:"experts_used,omitempty"`
	// Chat is whether the model ships a chat template (GGUF
	// tokenizer.chat_template, HF tokenizer_config.json or chat_template.jinja):
	// a model made to be talked to rather than only to continue text.
	Chat bool `json:"chat,omitempty"`
}

package setup

// ModelClass is what a service's model is, read by the host (isannd) from the
// weight files of the model running now: format, architecture, quantization
// and size. It rides on ServiceInfo.ModelClass into the register frame and out
// of RV's /v1/nodes.
//
// 🔴 A PEER CANNOT DERIVE THIS FROM Model. The name is whatever the file was
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
}

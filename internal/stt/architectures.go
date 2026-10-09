package stt

// transcribeCPPVersion is the speech library the runnable list below was taken from; the Rust
// core pins it in rust-ffi/Cargo.toml.
const transcribeCPPVersion = "0.3.1"

// runnable are the architectures, as a model's GGUF names them, the bundled library can transcribe
// with. Sortformer is left out: it tells speakers apart but never transcribes.
var runnable = map[string]bool{
	"canary":              true,
	"canary_qwen":         true,
	"cohere_asr":          true,
	"funasr_nano":         true,
	"gigaam":              true,
	"granite_speech":      true,
	"granite_speech5_ctc": true,
	"granite_speech_nar":  true,
	"medasr":              true,
	"moonshine":           true,
	"moonshine_streaming": true,
	"moss":                true,
	"parakeet":            true,
	"qwen3_asr":           true,
	"sensevoice":          true,
	"voxtral":             true,
	"voxtral_realtime":    true,
	"whisper":             true,
}

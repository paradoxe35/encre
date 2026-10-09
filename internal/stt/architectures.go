package stt

// Pinned in rust-ffi/Cargo.toml; the runnable list below was taken from this version.
const transcribeCPPVersion = "0.3.1"

// Sortformer is left out: it tells speakers apart but never transcribes.
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

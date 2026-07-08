// Package model sends model inference requests through Ollama endpoints.
//
// Ollama-only policy: Josh ruled on 2026-07-08 that model inference goes
// through Ollama only. Do not call direct provider APIs from this package or
// crawler model paths. Exceptions require explicit pre-approval.
package model

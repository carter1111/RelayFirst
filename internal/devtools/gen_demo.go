//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

func main() {
	const key = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
	agent, _ := receipt.DeriveAgentID(key, 8453)
	r := receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: "0x4f1a4e0d3c2b1a09f8e7d6c5b4a39281706958473a2b1c0d9e8f7a6b5c4d3e2f",
		AgentID:   agent,
		Epoch:     42,
		Task: receipt.Task{Type: receipt.TaskProbe,
			Spec:          map[string]any{"url": "https://api.example.com/health"},
			SpecHash:      "sha256:3d4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f",
			SelfGenerated: true},
		Work:   receipt.Work{Provider: "openai", Model: "gpt-5.5", TokensIn: 1234, TokensOut: 567, StartedAt: 1791015800, FinishedAt: 1791015862},
		Result: receipt.Result{Value: "200", Hash: "sha256:c1d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9"},
		Anchors: []receipt.Anchor{{URL: "https://api.example.com/health",
			ContentHash: "sha256:7b2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e",
			FetchedAt:   1791015810, Status: 200, Bytes: 20480}},
		Verification: receipt.Verification{Status: receipt.VerificationPending, Stake: 50},
	}
	if err := r.Sign(key); err != nil {
		panic(err)
	}
	out, _ := json.MarshalIndent(r, "", "  ")
	os.WriteFile("/tmp/rf-demo/receipt.json", out, 0600)
	fmt.Println("wrote /tmp/rf-demo/receipt.json")
}

//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	raw, _ := os.ReadFile(os.Args[1])
	var m map[string]any
	json.Unmarshal(raw, &m)
	r := m["result"].(map[string]any)
	r["value"] = "TAMPERED"
	out, _ := json.MarshalIndent(m, "", "  ")
	os.WriteFile(os.Args[2], out, 0600)
	fmt.Println("tampered result.value ->", r["value"])
}

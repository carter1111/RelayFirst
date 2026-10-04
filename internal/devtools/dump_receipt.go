//go:build ignore

package main

import (
	"fmt"
	"github.com/relayfirst/relayfirst/internal/store"
	"os"
)

func main() {
	db, err := store.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	s := store.NewReceiptStore(db)
	fmt.Println("receipts:", s.Count())
	rs, err := s.ByArtifact(os.Args[2])
	if err != nil {
		panic(err)
	}
	if len(rs) == 0 {
		fmt.Println("no receipt for artifact")
		return
	}
	b, _ := rs[0].MarshalCanonical()
	os.WriteFile("/tmp/rf-e2e/receipt.json", b, 0600)
	fmt.Println("wrote /tmp/rf-e2e/receipt.json")
}

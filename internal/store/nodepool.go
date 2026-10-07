package store

// NodePool combines the two Layer 0 inputs a settlement needs, so a caller can hand one
// value to mining.ScoringSink.Nodes instead of assembling them itself.
//
// It satisfies mining.NodePoolSource structurally -- the methods match, so no import of
// the mining package is needed here, which keeps the dependency pointing one way.
type NodePool struct {
	// Tenures supplies each node's tenure count.
	Tenures *SQLTenureLedger

	// Binds supplies each agent's bound node.
	Binds *SQLBindingLedger
}

// NewNodePool wires the two ledgers into one source.
func NewNodePool(tenures *SQLTenureLedger, binds *SQLBindingLedger) NodePool {
	return NodePool{Tenures: tenures, Binds: binds}
}

// NodeTenures returns each node's tenure count as of epoch.
func (p NodePool) NodeTenures(epoch uint64) (map[string]int, error) {
	return p.Tenures.AllTenures(epoch)
}

// Bindings returns each agent's bound node, for bindings made at or before epoch.
func (p NodePool) Bindings(epoch uint64) (map[string]string, error) {
	return p.Binds.Bindings(epoch)
}

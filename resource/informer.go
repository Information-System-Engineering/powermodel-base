package resource

// Informer mirrors kepler/internal/resource.Informer, narrowed to what this
// project reads.
//
// Kepler's interface also has Processes(), Containers() and VirtualMachines().
// cAdvisor gives us pods and nothing here reads the other three, so they are
// omitted; adding them later is mechanical. Refresh() keeps Kepler's
// argument-free signature — the timestamp arrives through the Clock.
type Informer interface {
	Init() error
	Refresh() error
	Node() *Node
	Pods() *Pods
}

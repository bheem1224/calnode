package abuse

// Gate bounds in-flight external work without building an unbounded waiting queue.
type Gate chan struct{}

func NewGate(limit int) Gate { return make(Gate, max(1, limit)) }
func (g Gate) Enter() bool {
	select {
	case g <- struct{}{}:
		return true
	default:
		return false
	}
}
func (g Gate) Leave() { <-g }

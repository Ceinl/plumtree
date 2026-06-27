package gateway

func (s *Server) acquireSlot() bool {
	if s.slots == nil {
		return true
	}
	select {
	case s.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseSlot() {
	if s.slots == nil {
		return
	}
	select {
	case <-s.slots:
	default:
	}
}

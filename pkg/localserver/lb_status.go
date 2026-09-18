package localserver

// SetLBStatus publishes an immutable snapshot for startup/status clients.
func (o *Owner) SetLBStatus(status LBStatus) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.settings.ReverseLB = &status
}

func (o *Owner) settingsSnapshot() ServerSettings {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.settings
}

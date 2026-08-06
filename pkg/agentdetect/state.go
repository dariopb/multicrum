package agentdetect

import "time"

type Provider string

const (
	ProviderCopilot Provider = "copilot"
	ProviderCrush   Provider = "crush"
)

type State string

const (
	StateUnknown State = "unknown"
	StateWorking State = "working"
	StateBlocked State = "blocked"
	StateDone    State = "done"
	StateIdle    State = "idle"
)

type Source string

const (
	SourceProcess Source = "process"
	SourceScreen  Source = "screen"
	SourceHook    Source = "hook"
	SourceNative  Source = "native"
)

type Confidence string

const (
	ConfidenceLow  Confidence = "low"
	ConfidenceHigh Confidence = "high"
)

type Status struct {
	Provider   Provider
	State      State
	Source     Source
	Confidence Confidence
	UpdatedAt  time.Time
}

type Target struct {
	ID         string
	Generation uint64
	ProcessID  int
	Local      bool
}

type Update struct {
	ID         string
	Generation uint64
	Status     *Status
}

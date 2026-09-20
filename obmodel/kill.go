package obmodel

type SessionKillFailure struct {
	SessionID int64
	Err       error
}

type BatchKillResult struct {
	Requested   int
	Succeeded   int
	AlreadyGone int
	Failures    []SessionKillFailure
}

type SessionDrainStopReason string

const (
	SessionDrainCompleted   SessionDrainStopReason = "completed"
	SessionDrainRoundLimit  SessionDrainStopReason = "round-limit"
	SessionDrainTargetLimit SessionDrainStopReason = "target-limit"
	SessionDrainTimeout     SessionDrainStopReason = "timeout"
	SessionDrainCancelled   SessionDrainStopReason = "cancelled"
	SessionDrainFatalError  SessionDrainStopReason = "fatal-error"
)

type SessionDrainProgress struct {
	Round         int
	MaxRounds     int
	Initial       int
	Current       int
	ProxyTargets  int
	DirectTargets int
}

type SessionDrainFailure struct {
	Command   string
	SessionID int64
	Err       error
}

type SessionDrainResult struct {
	User               string
	Rounds             int
	Initial            int
	Remaining          int
	ProxyCommands      int
	ConnectionCommands int
	AlreadyGone        int
	RebuiltIDs         int
	NewProxySessions   int
	Degraded           bool
	StopReason         SessionDrainStopReason
	Failures           []SessionDrainFailure
}

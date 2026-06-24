package eject

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/posit-dev/ptd/lib/types"
)

type CheckStatus string

const (
	CheckPass CheckStatus = "pass"
	CheckFail CheckStatus = "fail"
	CheckSkip CheckStatus = "skip"
)

type CheckResult struct {
	Name    string
	Status  CheckStatus
	Message string
}

type PreflightResult struct {
	Checks []CheckResult
}

func (r *PreflightResult) addCheck(name string, status CheckStatus, message string) {
	r.Checks = append(r.Checks, CheckResult{Name: name, Status: status, Message: message})
}

// Passed reports whether every check passed (no CheckFail). Warn/skip do not fail.
func (r *PreflightResult) Passed() bool {
	for _, c := range r.Checks {
		if c.Status == CheckFail {
			return false
		}
	}
	return true
}

// PreflightOptions configures which checks to run.
type PreflightOptions struct {
	Config            interface{}
	ControlRoomTarget types.Target // nil to skip control room reachability check
}

// RunPreflightChecks validates the workload is in a good state before eject.
func RunPreflightChecks(ctx context.Context, t types.Target, opts PreflightOptions) *PreflightResult {
	result := &PreflightResult{}

	checkControlRoomConfigured(result, opts.Config)
	checkCredentials(ctx, result, t)

	if opts.ControlRoomTarget != nil {
		checkControlRoomCredentials(ctx, result, opts.ControlRoomTarget)
	} else {
		result.addCheck("control_room_reachable", CheckSkip, "No control room target provided; skipping")
	}

	for _, c := range result.Checks {
		lvl := slog.LevelInfo
		if c.Status == CheckFail {
			lvl = slog.LevelError
		}
		slog.Log(ctx, lvl, "Preflight check", "name", c.Name, "status", c.Status, "message", c.Message)
	}

	return result
}

func checkControlRoomConfigured(result *PreflightResult, config interface{}) {
	var hasFields bool

	switch cfg := config.(type) {
	case types.AWSWorkloadConfig:
		hasFields = cfg.ControlRoomAccountID != "" || cfg.ControlRoomDomain != "" ||
			cfg.ControlRoomClusterName != "" || cfg.ControlRoomRegion != ""
	case types.AzureWorkloadConfig:
		hasFields = cfg.ControlRoomAccountID != "" || cfg.ControlRoomDomain != "" ||
			cfg.ControlRoomClusterName != "" || cfg.ControlRoomRegion != ""
	default:
		result.addCheck("control_room_configured", CheckFail, fmt.Sprintf("Unsupported config type: %T", config))
		return
	}

	if !hasFields {
		result.addCheck("control_room_configured", CheckFail,
			"No control_room_* fields found in config; workload may already be ejected")
		return
	}

	result.addCheck("control_room_configured", CheckPass, "Control room fields are present in config")
}

func checkCredentials(ctx context.Context, result *PreflightResult, t types.Target) {
	// Target.Credentials already refreshes the credentials internally, so a
	// successful return validates them; no separate Refresh call is needed.
	if _, err := t.Credentials(ctx); err != nil {
		result.addCheck("workload_credentials", CheckFail,
			fmt.Sprintf("Failed to get workload credentials: %v", err))
		return
	}

	result.addCheck("workload_credentials", CheckPass, "Workload credentials are valid")
}

func checkControlRoomCredentials(ctx context.Context, result *PreflightResult, controlRoom types.Target) {
	// Target.Credentials already refreshes the credentials internally, so a
	// successful return validates them; no separate Refresh call is needed.
	if _, err := controlRoom.Credentials(ctx); err != nil {
		result.addCheck("control_room_reachable", CheckFail,
			fmt.Sprintf("Failed to get control room credentials: %v", err))
		return
	}

	result.addCheck("control_room_reachable", CheckPass, "Control room credentials are valid")
}

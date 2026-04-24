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
	CheckWarn CheckStatus = "warn"
	CheckSkip CheckStatus = "skip"
)

type CheckResult struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message"`
}

type PreflightResult struct {
	Checks []CheckResult `json:"checks"`
	Passed bool          `json:"passed"`
}

func (r *PreflightResult) addCheck(name string, status CheckStatus, message string) {
	r.Checks = append(r.Checks, CheckResult{Name: name, Status: status, Message: message})
}

func (r *PreflightResult) computePassed() {
	r.Passed = true
	for _, c := range r.Checks {
		if c.Status == CheckFail {
			r.Passed = false
			return
		}
	}
}

// PreflightOptions configures which checks to run.
type PreflightOptions struct {
	Config            interface{}
	ControlRoomTarget types.Target // nil to skip control room reachability check
}

// RunPreflightChecks validates the workload is in a good state before eject.
func RunPreflightChecks(ctx context.Context, t types.Target, opts PreflightOptions) (*PreflightResult, error) {
	result := &PreflightResult{}

	checkControlRoomConfigured(result, opts.Config)
	checkCredentials(ctx, result, t)

	if opts.ControlRoomTarget != nil {
		checkControlRoomCredentials(ctx, result, opts.ControlRoomTarget)
	} else {
		result.addCheck("control_room_reachable", CheckSkip, "No control room target provided; skipping")
	}

	result.computePassed()

	for _, c := range result.Checks {
		lvl := slog.LevelInfo
		if c.Status == CheckFail {
			lvl = slog.LevelError
		} else if c.Status == CheckWarn {
			lvl = slog.LevelWarn
		}
		slog.Log(ctx, lvl, "Preflight check", "name", c.Name, "status", c.Status, "message", c.Message)
	}

	return result, nil
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
	creds, err := t.Credentials(ctx)
	if err != nil {
		result.addCheck("workload_credentials", CheckFail,
			fmt.Sprintf("Failed to get workload credentials: %v", err))
		return
	}

	if err := creds.Refresh(ctx); err != nil {
		result.addCheck("workload_credentials", CheckFail,
			fmt.Sprintf("Workload credentials failed validation: %v", err))
		return
	}

	result.addCheck("workload_credentials", CheckPass, "Workload credentials are valid")
}

func checkControlRoomCredentials(ctx context.Context, result *PreflightResult, controlRoom types.Target) {
	creds, err := controlRoom.Credentials(ctx)
	if err != nil {
		result.addCheck("control_room_reachable", CheckFail,
			fmt.Sprintf("Failed to get control room credentials: %v", err))
		return
	}

	if err := creds.Refresh(ctx); err != nil {
		result.addCheck("control_room_reachable", CheckFail,
			fmt.Sprintf("Control room credentials failed validation: %v", err))
		return
	}

	result.addCheck("control_room_reachable", CheckPass, "Control room credentials are valid")
}

package pulumi

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optrefresh"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/rstudio/ptd/lib/helpers"
)

func RefreshStack(ctx context.Context, stack auto.Stack) (refreshResult auto.RefreshResult, err error) {
	refreshResult, err = stack.Refresh(ctx,
		optrefresh.Color("always"),
		optrefresh.ErrorProgressStreams(os.Stderr),
		optrefresh.ProgressStreams(os.Stderr),
		optrefresh.SuppressOutputs(),
		optrefresh.Diff(),
		optrefresh.ClearPendingCreates(),
	)
	if err != nil {
		// don't return the full error, it's already printed to stderr.
		return refreshResult, fmt.Errorf("failed to refresh stack, error above")
	}
	return
}

func Whoami(ctx context.Context, stack auto.Stack) error {
	stackName := stack.Name()

	stackDetails, err := stack.Workspace().WhoAmIDetails(ctx)
	if err != nil {
		slog.Error("Failed to get Pulumi account info", "error", err)
		return err
	}

	slog.Info("Pulumi whoami details", "stack", stackName, "user", stackDetails.User, "url", stackDetails.URL)

	return nil
}

func CancelStack(ctx context.Context, stack auto.Stack) (err error) {
	err = stack.Cancel(ctx)
	if err != nil {
		return
	}
	slog.Info("Cancelled stack")
	return
}

func UpStack(ctx context.Context, stack auto.Stack, preview bool, dryRun bool, autoApply bool, excludedResources []string, targetResources []string) (upResult auto.UpResult, err error) {
	if preview {
		res, err2 := previewStack(ctx, excludedResources, targetResources, stack)

		if err2 != nil {
			// don't return the full error, it's already printed to stderr.
			return upResult, fmt.Errorf("failed to preview stack, error above")
		}

		// remove "same", those aren't changes.
		delete(res.ChangeSummary, "same")

		if len(res.ChangeSummary) > 0 && !dryRun {
			if !autoApply && !helpers.AskForConfirmation("Changes expected, proceeding with up?") {
				slog.Info("Exiting without up")
				return
			}
		} else {
			slog.Info("No changes expected, exiting without up")
			return
		}
	}

	// bail if dry run, regardless of preview-ness
	if dryRun {
		slog.Info("Dry run, exiting without up")
		return
	}

	// Build up options list
	upOptions := []optup.Option{
		optup.Color("always"),
		optup.ErrorProgressStreams(os.Stderr),
		optup.ProgressStreams(os.Stderr),
		optup.SuppressOutputs(),
		optup.Diff(),
	}

	if len(excludedResources) > 0 {
		slog.Info("Excluding resources from up", "excluded_resources", excludedResources)
		upOptions = append(upOptions, optup.Exclude(excludedResources))
	} else {
		slog.Info("No resources excluded from up")
	}

	if len(targetResources) > 0 {
		slog.Info("Targeting specific resources for up", "target_resources", targetResources)
		upOptions = append(upOptions, optup.Target(targetResources))
	} else {
		slog.Info("No specific resources targeted for up")
	}

	upResult, err = stack.Up(ctx, upOptions...)
	if err != nil {
		// don't return the full error, it's already printed to stderr.
		return upResult, fmt.Errorf("failed to up stack, error above")
	}

	return
}

func DestroyStack(ctx context.Context, stack auto.Stack, preview bool, dryRun bool, excludedResources []string, targetResources []string) (destroyResult auto.DestroyResult, err error) {
	if preview {
		// For destroy, we want to know if there are any resources to destroy
		// Check if there are any resources in the current state
		outputs, outputsErr := stack.Outputs(ctx)
		if outputsErr != nil {
			slog.Warn("Could not get stack outputs", "error", outputsErr)
		}

		// Show information about what will be destroyed
		if len(outputs) > 0 {
			slog.Info("Stack contains resources that will be destroyed")
		} else {
			slog.Info("Stack appears to be empty, but destroy will proceed to ensure cleanup")
		}

		// Ask for confirmation after showing the preview
		if !dryRun {
			if !helpers.AskForConfirmation("This will destroy the entire stack and all its resources. Proceed with destroy?") {
				slog.Info("Exiting without destroy")
				return
			}
		}
	}

	// bail if dry run, regardless of preview-ness
	if dryRun {
		slog.Info("Dry run, exiting without destroy")
		return
	}

	// Build up options list
	destroyOptions := []optdestroy.Option{
		optdestroy.Color("always"),
		optdestroy.ErrorProgressStreams(os.Stderr),
		optdestroy.ProgressStreams(os.Stderr),
		optdestroy.SuppressOutputs(),
	}

	if len(excludedResources) > 0 {
		slog.Info("Excluding resources from destroy", "excluded_resources", excludedResources)
		destroyOptions = append(destroyOptions, optdestroy.Exclude(excludedResources))
	} else {
		slog.Info("No resources excluded from destroy")
	}

	if len(targetResources) > 0 {
		slog.Info("Targeting specific resources for destroy", "target_resources", targetResources)
		destroyOptions = append(destroyOptions, optdestroy.Target(targetResources))
	} else {
		slog.Info("No specific resources targeted for destroy")
	}

	destroyResult, err = stack.Destroy(ctx, destroyOptions...)
	if err != nil {
		// don't return the full error, it's already printed to stderr.
		return destroyResult, fmt.Errorf("failed to destroy stack, error above")
	}

	return
}

func k8sEnvVars() map[string]string {
	// Default pulumi+k8s env vars copied from the python cli expectations.
	return map[string]string{
		"PULUMI_K8S_DELETE_UNREACHABLE": "false",
		// NOTE: Using server-side apply is the default in pulumi-kubernetes v4+, so having
		//   this here is mostly a breadcrumb to finding this comment and reference link:
		//   https://www.pulumi.com/registry/packages/kubernetes/how-to-guides/managing-resources-with-server-side-apply/#enable-server-side-apply
		"PULUMI_K8S_ENABLE_SERVER_SIDE_APPLY": "true",

		// NOTE: Using the patch force option is separate from approving a plan update, so
		//   if the changes look undesirable, setting "PULUMI_K8S_ENABLE_PATCH_FORCE=false"
		//   in the outer environment will be sufficient to choose a different adventure.
		//   https://www.pulumi.com/registry/packages/kubernetes/how-to-guides/managing-resources-with-server-side-apply/#handle-field-conflicts-on-existing-resources
		"PULUMI_K8S_ENABLE_PATCH_FORCE": "true",
	}
}

func previewStack(ctx context.Context, excludedResources []string, targetResources []string, stack auto.Stack) (previewResult auto.PreviewResult, err error) {
	// Build up options list
	previewOptions := []optpreview.Option{
		optpreview.Color("always"),
		optpreview.ErrorProgressStreams(os.Stderr),
		optpreview.ProgressStreams(os.Stderr),
		optpreview.SuppressOutputs(),
		optpreview.Diff(),
	}

	if len(excludedResources) > 0 {
		slog.Info("Excluding resources from preview", "excluded_resources", excludedResources)
		previewOptions = append(previewOptions, optpreview.Exclude(excludedResources))
	} else {
		slog.Info("No resources excluded from preview")
	}

	if len(targetResources) > 0 {
		slog.Info("Targeting specific resources for preview", "target_resources", targetResources)
		previewOptions = append(previewOptions, optpreview.Target(targetResources))
	} else {
		slog.Info("No specific resources targeted for preview")
	}

	previewResult, err = stack.Preview(ctx, previewOptions...)
	if err != nil {
		return previewResult, fmt.Errorf("failed to preview stack, error above")
	}

	return previewResult, nil
}

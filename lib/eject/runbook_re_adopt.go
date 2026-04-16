package eject

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

type reAdoptData struct {
	TargetName  string
	AccountID   string
	ClusterName string
	Domain      string
	Region      string
}

var reAdoptTemplate = strings.Join([]string{
	"# Re-Adopting {{.TargetName}} into the Control Room",
	"",
	"This runbook describes how to bring the ejected workload " + "`" + "{{.TargetName}}" + "`" + " back under",
	"control room management.",
	"",
	"## Prerequisites",
	"",
	"- Access to the workload's ptd.yaml configuration",
	"- The PTD CLI installed and configured",
	"- AWS credentials with access to both the workload and control room accounts",
	"",
	"## Original Control Room Configuration",
	"",
	"These values were captured at eject time. If the control room has changed since",
	"eject, use the updated values instead.",
	"",
	"| Field | Value |",
	"|-------|-------|",
	"| " + "`" + "control_room_account_id" + "`" + " | " + "`" + "{{.AccountID}}" + "`" + " |",
	"| " + "`" + "control_room_cluster_name" + "`" + " | " + "`" + "{{.ClusterName}}" + "`" + " |",
	"| " + "`" + "control_room_domain" + "`" + " | " + "`" + "{{.Domain}}" + "`" + " |",
	"| " + "`" + "control_room_region" + "`" + " | " + "`" + "{{.Region}}" + "`" + " |",
	"",
	"## Procedure",
	"",
	"### 1. Restore control room configuration",
	"",
	"Edit " + "`" + "ptd.yaml" + "`" + " and restore the " + "`" + "control_room_*" + "`" + " fields under the " + "`" + "spec:" + "`" + " section:",
	"",
	"```yaml",
	"spec:",
	`  control_room_account_id: "{{.AccountID}}"`,
	`  control_room_cluster_name: "{{.ClusterName}}"`,
	`  control_room_domain: "{{.Domain}}"`,
	`  control_room_region: "{{.Region}}"`,
	"```",
	"",
	"### 2. Run full ensure",
	"",
	"Run a full ensure to re-establish all control room connections:",
	"",
	"```",
	"ptd ensure {{.TargetName}}",
	"```",
	"",
	"This will:",
	"- Re-create and sync the Mimir authentication password to the control room",
	`- Re-enable the Alloy ` + "`" + `prometheus.remote_write "control_room"` + "`" + ` block for metrics`,
	"- Converge all infrastructure to the connected state",
	"",
	"### 3. Verify",
	"",
	"After ensure completes, verify the connections are working:",
	"",
	"- **Metrics**: Check that metrics are flowing to the control room Mimir at " + "`" + "https://mimir.{{.Domain}}" + "`",
	"- **Pods**: Verify all pods are running: " + "`" + "ptd workon {{.TargetName}} -- kubectl get pods -A" + "`",
	"- **Alloy**: Check Alloy logs for successful remote_write: " + "`" + "ptd workon {{.TargetName}} -- kubectl logs -n alloy -l app.kubernetes.io/name=alloy --tail=50" + "`",
	"",
	"## Known Gotchas",
	"",
	"- **Re-adopt is cleanest within 30 days of eject.** Longer gaps increase the chance of drift between the workload and control room.",
	"- **Team Operator version drift.** If the control room has upgraded Team Operator since eject, the re-adopted workload may need an upgrade too. The ensure will handle this if the chart version is pinned in the control room config.",
	"- **Manual infrastructure changes.** If manual changes were made to the workload's infrastructure during the ejected period, the ensure may report unexpected diffs. Review the Pulumi preview before applying.",
	"- **Control room changes.** If the control room moved to a different account, domain, or region, use the new values instead of the ones listed above.",
	"- **IAM trust.** If the " + "`" + "admin.posit.team" + "`" + " role trust was removed (per the remove-posit-access runbook), it must be re-established before re-adoption. Contact Posit for assistance.",
	"",
}, "\n")

func WriteReAdoptRunbook(outputDir string, details *ControlRoomDetails, targetName string) error {
	tmpl, err := template.New("re-adopt").Parse(reAdoptTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse re-adopt template: %w", err)
	}

	runbooksDir := filepath.Join(outputDir, "runbooks")
	if err := os.MkdirAll(runbooksDir, 0755); err != nil {
		return fmt.Errorf("failed to create runbooks directory: %w", err)
	}

	data := reAdoptData{
		TargetName:  targetName,
		AccountID:   details.AccountID,
		ClusterName: details.ClusterName,
		Domain:      details.Domain,
		Region:      details.Region,
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to render re-adopt runbook: %w", err)
	}

	path := filepath.Join(runbooksDir, "re-adopt.md")
	if err := os.WriteFile(path, []byte(buf.String()), 0644); err != nil {
		return fmt.Errorf("failed to write re-adopt runbook: %w", err)
	}

	return nil
}

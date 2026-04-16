# Re-Adopting an Ejected Workload

After running `ptd eject` on a workload, the control room connections are severed. This guide covers how to bring the workload back under control room management.

This is primarily useful for:
- Testing the eject feature and resetting state afterward
- Reversing an eject during a transition period
- Migrating a workload from one control room to another

## Prerequisites

- Access to the workload's ptd.yaml
- The PTD CLI installed and configured
- AWS credentials for both the workload and control room accounts
- The original control room config values (captured in the eject bundle's `metadata.json` under `control_room_snapshot`)

## Procedure

### 1. Restore control room configuration

Edit the workload's `ptd.yaml` and restore the `control_room_*` fields under `spec:`:

```yaml
spec:
  control_room_account_id: "<account-id>"
  control_room_cluster_name: "<cluster-name>"
  control_room_domain: "<domain>"
  control_room_region: "<region>"
```

The original values are in the eject artifact bundle at `metadata.json`. If the control room has changed since eject, use the current values instead.

### 2. Run full ensure

```
ptd ensure <target>
```

This will:
- Re-create and sync the Mimir authentication password to the control room
- Re-enable the Alloy `prometheus.remote_write "control_room"` block for metrics
- Converge all infrastructure to the connected state

### 3. Verify

```
ptd workon <target> -- bash -c "kubectl get pods -A | grep -E 'alloy|mimir'"
```

Check that:
- Metrics are flowing to the control room Mimir at `https://mimir.<domain>`
- Alloy pods are running without errors related to remote_write
- All workload pods are healthy

## Migrating to a different control room

The same procedure works for moving a workload from one control room to another. Run `ptd eject` against the old control room first — this cleans up the workload's Mimir password entry from the old control room's Secrets Manager and severs the Alloy metrics pipeline. Then follow the re-adopt procedure above using the *new* control room's values instead of the original ones.

## Known gotchas

- **Re-adopt is cleanest within 30 days of eject.** Longer gaps increase the chance of drift between the workload and control room.
- **Team Operator version drift.** If the control room has upgraded Team Operator since eject, the re-adopted workload may need an upgrade too. The ensure will handle this if the chart version is pinned in the control room config.
- **Manual infrastructure changes.** If manual changes were made to the workload during the ejected period, the ensure may report unexpected diffs. Review the Pulumi preview before applying.
- **IAM trust.** If the `admin.posit.team` role trust was removed (per the eject bundle's remove-posit-access runbook), it must be re-established before re-adoption.
